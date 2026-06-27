package server

import (
	"context"
	"flux/demo"
	"flux/internal/config"
	"flux/internal/consts"
	"flux/tool/builtin"
	"fmt"
	"log"
	"strings"
	"time"

	"flux/adapter/postgres"
	"flux/cost"
	"flux/engine"
	"flux/eventbus"
	"flux/handler"
	"flux/pkg/lock"
	"flux/registry"
	"flux/repository"
	query2 "flux/repository/query"
	"flux/service"
	"flux/tool"
	"flux/websocket"
	worker2 "flux/worker"
	workflow2 "flux/workflow"
	"flux/workflow/nodes"

	"github.com/gin-gonic/gin"
	websocket2 "github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Server struct {
	wsHub *websocket.WSHub

	WorkflowRepo        repository.WorkflowRepository
	WorkflowVersionRepo repository.WorkflowVersionRepository
	TaskRepo            repository.TaskRepository
	TaskEventRepo       repository.EventRepository
	NodeRuntimeRepo     repository.NodeRuntimeRepository
	TaskCostTraceRepo   repository.TaskCostTraceRepository
	AwaitBindingRepo    repository.AwaitBindingRepository

	WorkflowHandler          *handler.WorkflowHandler
	AwaitHandler             *handler.AwaitHandler
	AliyunEventBridgeHandler *handler.AliyunEventBridgeHandler
	AwaitReplayHandler       *handler.AwaitReplayHandler
	runInspectorHandler      *handler.RunInspectorHandler
}

func NewServer(db *gorm.DB, rdb *redis.Client, cfg *config.Config) *Server {
	queue := query2.NewRedisQueue(
		rdb,
		"video_task_queue",
		"video_task_processing",
		"video_task_dead",
		2*time.Second,
	)

	taskRepo := query2.NewTaskRepository(db, queue)
	eventRepo := query2.NewEventRepository(db)
	workflowVersionRepo := query2.NewWorkflowVersionRepository(db)
	workflowRepo := query2.NewWorkflowRepository(db)
	nodeRuntimeRepo := query2.NewNodeRuntimeRepository(db)
	taskCostTraceRepo := query2.NewTaskCostTraceRepository(db)
	awaitBindingRepo := query2.NewAwaitBindingRepository(db)

	// ── v3 Store 适配器：将 GORM repository 包装为 flux v3 Store 接口 ──
	pgWorkflowStore := postgres.NewWorkflowStore(nodeRuntimeRepo, taskRepo)
	pgAwaitStore := postgres.NewAwaitStore(awaitBindingRepo)
	pgTraceStore := postgres.NewTraceStore(db)
	_ = pgWorkflowStore // v3: 注入到 flux.Config.WorkflowStore
	_ = pgAwaitStore    // v3: 注入到 flux.Config.AwaitStore
	_ = pgTraceStore    // v3: 注入到 flux.Config.TraceStore

	wsHub := websocket.NewWSHub(websocket.NewRepositoryTaskAccessChecker(taskRepo))

	jobQueue := engine.NewRedisStreamJobQueue(
		rdb,
		"workflow_jobs",
		"workflow_group",
	)

	eventBus := eventbus.NewEventBus(
		eventRepo,
		wsHub,
	)

	//--------------------------------
	// Tool Registry
	//--------------------------------
	toolRegistry := tool.NewRegistry()

	toolRegistry.Register(builtin.NewMergeResultTool())

	nodeRegistry := nodes.InitNodeRegistry(toolRegistry)

	// 工作流注册
	wfRegistry := registry.NewWorkflowRegistry(
		workflowRepo,
		workflowVersionRepo,
	)
	wfRegistry.Register(demo.AwaitUserChoiceDemoWorkflow())
	ctx := context.Background()

	// 同步工作流程模版到数据库
	err := wfRegistry.Sync(ctx)
	if err != nil {
		panic(err)
	}

	// 构建工作流
	builder := workflow2.NewBuilder(nodeRegistry)

	// 创建异步worker
	asyncWorker := worker2.NewAsyncWorker(
		jobQueue,
		taskRepo,
		nodeRuntimeRepo,
		toolRegistry,
		eventBus,
	)
	dLocker := lock.NewRedisLock(rdb)

	// 初始化engine
	eng := engine.NewEngine(
		taskRepo,
		nodeRuntimeRepo,
		awaitBindingRepo,
		workflowVersionRepo,
		workflowRepo,
		builder,
		eventBus,
		jobQueue,
		dLocker,
		eventRepo,
	)
	eng.SetCostRecorder(cost.NewTaskCostTraceRecorder(taskRepo, taskCostTraceRepo))
	eng.SetSubWorkflowBinding(cfg.AiEngine.SubWorkflowAwaitBinding)
	awaitPollWorker := worker2.NewAwaitPollWorker(
		awaitBindingRepo,
		toolRegistry,
		eng,
		eventBus,
		dLocker,
	)

	taskRetryService := service.NewTaskRetryService(
		workflowVersionRepo,
		taskRepo,
		nodeRuntimeRepo,
		awaitBindingRepo,
		builder,
	)
	//billingEntitlementSvc := internalservice.NewBillingEntitlementService(
	//	query.NewUserSubscriptionRepo(db),
	//	query.NewUserMembershipPeriodRepo(db),
	//	query.NewBillingProductRepo(db),
	//	query.NewUserDailyUsageStatRepo(db),
	//)
	//billingPricingRuleSvc := internalservice.NewBillingPricingRuleService(
	//	query.NewBillingPricingRuleRepo(db),
	//)
	//billingExpirationSvc := NewBillingExpirationService(db)
	//billingTaskSvc := internalservice.NewBillingTaskService(db, billingEntitlementSvc, billingPricingRuleSvc, billingExpirationSvc)
	taskBillingSettlementListener := service.NewTaskBillingSettlementListener(nil, eventBus, taskRepo)

	w := worker2.NewWorker(
		eng,
		taskRepo,
		nodeRuntimeRepo,
		workflowVersionRepo,
		workflowRepo,
		queue,
		jobQueue,
		eventBus,
		nodeRegistry,
		dLocker,
		builder,
		taskRetryService,
	)

	go w.Loop(ctx)
	go w.Loop(ctx)
	go w.Loop(ctx)
	go worker2.TaskQueueRecovery(queue, ctx)
	worker2.StartAsyncWorkers(ctx, asyncWorker, 4)
	worker2.StartAwaitPollWorkers(ctx, awaitPollWorker, 1)
	taskBillingSettlementListener.Start(ctx, eventBus)

	taskService := service.NewTaskForkService(
		taskRepo,
		workflowVersionRepo,
		builder, eng,
	)

	nodeReplaySvc := service.NewNodeReplayService(
		taskRepo,
		nodeRuntimeRepo,
		workflowVersionRepo,
		eng,
		toolRegistry,
	)

	workflowHandler := handler.NewWorkflowHandler(
		workflowRepo,
		workflowVersionRepo,
		taskRepo,
		taskCostTraceRepo,
		eventRepo,
		nodeRuntimeRepo,
		builder,
		taskService,
		taskRetryService,
		nil,
		eng,
	).WithNodeReplayService(nodeReplaySvc)
	awaitHandler := handler.NewAwaitHandler(eng, awaitBindingRepo)
	aliyunEventBridgeService := service.NewAliyunEventBridgeService(eng, awaitBindingRepo, toolRegistry, eventBus)
	aliyunEventBridgeHandler := handler.NewAliyunEventBridgeHandler(aliyunEventBridgeService)
	awaitReplayService := service.NewAwaitReplayService(eng, awaitBindingRepo, toolRegistry, eventBus)
	awaitReplayEnabled := strings.EqualFold(strings.TrimSpace(cfg.Server.Mode), "debug")
	awaitReplayHandler := handler.NewAwaitReplayHandler(awaitReplayService, awaitReplayEnabled)

	runInspectorHandler := handler.NewRunInspectorHandler(
		eng,
		taskRepo,
		nodeRuntimeRepo,
		eventRepo,
		awaitBindingRepo,
		workflowRepo,
		workflowVersionRepo,
		builder,
		taskService,
	)

	return &Server{
		wsHub: wsHub,

		WorkflowRepo:             workflowRepo,
		WorkflowVersionRepo:      workflowVersionRepo,
		TaskRepo:                 taskRepo,
		TaskEventRepo:            eventRepo,
		NodeRuntimeRepo:          nodeRuntimeRepo,
		TaskCostTraceRepo:        taskCostTraceRepo,
		AwaitBindingRepo:         awaitBindingRepo,
		WorkflowHandler:          workflowHandler,
		AwaitHandler:             awaitHandler,
		AliyunEventBridgeHandler: aliyunEventBridgeHandler,
		AwaitReplayHandler:       awaitReplayHandler,
		runInspectorHandler:      runInspectorHandler,
	}
}

// RegisterRoutes 注册内部Api路由
func (s *Server) wsHandler(c *gin.Context) {
	if !websocket2.IsWebSocketUpgrade(c.Request) {
		log.Println("Not websocket upgrade request")
	}
	userID := c.GetInt64(consts.UserID)
	deviceType := c.GetString(consts.DeviceType)

	s.wsHub.ServeWS(c.Writer, c.Request, fmt.Sprintf("%v", userID), deviceType)
}

func (s *Server) RegisterRoutes(rg *gin.RouterGroup) {
	wh := s.WorkflowHandler
	ah := s.AwaitHandler
	arh := s.AwaitReplayHandler
	rih := s.runInspectorHandler

	workflow := rg.Group("/workflows")
	{
		workflow.POST("", wh.CreateWorkflow)
		workflow.GET("", wh.ListWorkflows)
		workflow.GET("/:id", wh.GetWorkflow)
		workflow.POST("/:id/run", wh.RunWorkflow)
	}

	task := rg.Group("/tasks")
	{
		task.GET("", wh.GetTasksByUser)
		task.GET("/:id", wh.GetTask)
		//task.GET("/:id/creative-detail", wh.GetTaskCreativeDetail)
		//task.GET("/:id/timeline", wh.GetTaskVideoTimeline)
		task.POST("", wh.CreateTaskFromWorkflow)
		task.POST("/resume", wh.ResumeTask)
		task.POST("/:id/fork", wh.ForkTask)
		task.GET("/:id/events", wh.GetEvents)
		task.GET("/:id/nodes/:node/children", wh.GetChildrenByNode)
		task.POST("/:id/nodes/:node/replay-input", wh.ReplayTaskNodeInput)
		task.POST("/:id/nodes/:node/replay", wh.ReplayTaskNode)
		task.GET("/:id/replay", wh.ReplayTask)
		task.POST("/:id/replay/stream", wh.ReplayTaskStream)
	}

	await := rg.Group("/await")
	{
		await.POST("/signals", ah.HandleSignal)
	}

	if arh != nil {
		internalAwait := rg.Group("/internal/await")
		{
			internalAwait.POST("/providers/:provider/replay", arh.HandleProviderReplay)
		}
	}

	run := rg.Group("/runs")
	{
		run.GET("", rih.ListRuns)
		run.GET("/:id/inspect", rih.GetRunInspector)
		run.GET("/:id/dag", rih.GetRunDAG)
		run.GET("/:id/timeline", rih.GetRunTimeline)
		run.GET("/:id/nodes/:node", rih.GetRunNodeDetail)
		run.GET("/:id/nodes/:node/diff", rih.GetRunNodeDiff)
		run.GET("/:id/nodes/:node/expansion", rih.GetRunNodeExpansion)
		run.POST("/:id/patch-preview", rih.PatchPreview)
		// 真正创建分叉运行
		run.POST("/:id/redo", rih.RedoRun)
	}

	rg.GET("/ws", s.wsHandler)

}

func (s *Server) RegisterWebhookRoutes(rg *gin.RouterGroup) {
	ah := s.AwaitHandler
	aeh := s.AliyunEventBridgeHandler

	await := rg.Group("/await")
	{
		await.POST("/aliyun/eventbridge", aeh.HandleAsyncTaskFinish)
		await.POST("/:provider", ah.HandleProviderWebhook)
	}
}
