package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server        `yaml:"server" mapstructure:"server"`
	Database      `yaml:"database" mapstructure:"database"`
	Redis         `yaml:"redis" mapstructure:"redis"`
	Log           `yaml:"log" mapstructure:"log"`
	Auth          `yaml:"auth" mapstructure:"auth"`
	DeepSeek      LLM           `yaml:"deepseek" mapstructure:"deepseek"`
	OpenAI        LLM           `yaml:"openai" mapstructure:"openai"`
	Qwen          LLM           `yaml:"qwen" mapstructure:"qwen"`
	AliOSS        AliOSS        `yaml:"alioss" mapstructure:"alioss"`
	AliSTS        AliSTS        `yaml:"alists" mapstructure:"alists"`
	AliyunAuth    AliyunAuth    `yaml:"aliyunauth" mapstructure:"aliyunauth"`
	AppleAuth     AppleAuth     `yaml:"appleauth" mapstructure:"appleauth"`
	Volcengine    Volcengine    `yaml:"volcengine" mapstructure:"volcengine"`
	Keling        Keling        `yaml:"keling" mapstructure:"keling"`
	AppStore      AppStore      `yaml:"appstore" mapstructure:"appstore"`
	TTS           TTS           `yaml:"tts" mapstructure:"tts"`
	FFmpeg        FFmpegConfig  `yaml:"ffmpeg" mapstructure:"ffmpeg"`
	VolcengineTTS VolcengineTTS `yaml:"volcengineTTS" mapstructure:"volcengineTTS"`

	AnonymousCleanup AnonymousCleanup `yaml:"anonymous_cleanup" mapstructure:"anonymous_cleanup"`
	AiEngine         AiEngine         `yaml:"ai_engine" mapstructure:"ai_engine"`
}

// AiEngine DAG 引擎相关的运行期开关。
type AiEngine struct {
	// SubWorkflowAwaitBinding 开启 subworkflow-as-await-binding 写入路径（P1）。
	// 默认 false：父节点停在 NodeRunning，由 recovery_scanner 兜底（改造前行为）。
	// true：父节点挂起落 NodeAwaiting + 建 await binding，完成走 CompleteAwaitNode。
	// ⚠️ P2 的 poll 对账兜底上线前，仅供灰度验证（丢事件/崩溃场景缺兜底）。
	SubWorkflowAwaitBinding bool `yaml:"subworkflow_await_binding" mapstructure:"subworkflow_await_binding"`
}

// AnonymousCleanup 控制匿名账号的定期清理任务。
// 长期未升级、无订阅、无订单的匿名账号会被硬删，避免 users 表无限膨胀。
// 默认禁用；生产开启前请在测试环境验证清理条件。
type AnonymousCleanup struct {
	Enabled       bool  `yaml:"enabled" mapstructure:"enabled"`
	RetentionDays int64 `yaml:"retention_days" mapstructure:"retention_days"`
	IntervalHours int64 `yaml:"interval_hours" mapstructure:"interval_hours"`
	BatchSize     int   `yaml:"batch_size" mapstructure:"batch_size"`
}

type Server struct {
	Port     int    `yaml:"port" mapstructure:"port"`
	Mode     string `yaml:"mode" mapstructure:"mode"`
	Language string `yaml:"language" mapstructure:"language"`
	BaseURL  string `yaml:"baseurl" mapstructure:"baseurl"`
}

type Database struct {
	Host     string `yaml:"host" mapstructure:"host"`
	Port     int    `yaml:"port" mapstructure:"port"`
	User     string `yaml:"user" mapstructure:"user"`
	Password string `yaml:"password" mapstructure:"password"`
	DBName   string `yaml:"dbname" mapstructure:"dbname"`
	SSLMode  string `yaml:"sslmode" mapstructure:"sslmode"`
}

type Log struct {
	Level     string `yaml:"level" mapstructure:"level"`
	Format    string `yaml:"format" mapstructure:"format"`
	AddSource bool   `yaml:"add_source" mapstructure:"add_source"`
}

type Redis struct {
	Host         string        `yaml:"host" mapstructure:"host"`
	Port         int           `yaml:"port" mapstructure:"port"`
	Password     string        `yaml:"password" mapstructure:"password"`
	DB           int           `yaml:"db" mapstructure:"db"`
	PoolSize     int           `yaml:"poolsize" mapstructure:"poolsize"`
	MinIdleConns int           `yaml:"minidleconns" mapstructure:"minidleconns"`
	IdleTimeout  time.Duration `yaml:"idletimeout" mapstructure:"idletimeout"`
}

type Auth struct {
	JWTSecret       string `yaml:"jwtsecret" mapstructure:"jwtsecret"`
	TTL             int64  `yaml:"ttl" mapstructure:"ttl"`
	BlacklistPeriod int64  `yaml:"blacklistperiod" mapstructure:"blacklistperiod"` // // 黑名单宽限时间（秒）
}

func NewConfig(path string) (*Config, error) {
	return loadConfig(path)
}

// LoadConfig 解析配置文件
func loadConfig(path string) (*Config, error) {
	viper.SetConfigFile(path)
	viper.SetConfigType("yaml")
	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败:%v", err)
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败:%v", err)
	}
	cfg.applyLegacyCompat()

	return &cfg, nil
}

func (c *Config) applyLegacyCompat() {
	if c == nil {
		return
	}
	if strings.TrimSpace(c.TTS.Volcengine.AppID) == "" &&
		strings.TrimSpace(c.TTS.Volcengine.AccessToken) == "" &&
		strings.TrimSpace(c.TTS.Volcengine.VoiceType) == "" &&
		(strings.TrimSpace(c.VolcengineTTS.AppID) != "" ||
			strings.TrimSpace(c.VolcengineTTS.AccessToken) != "" ||
			strings.TrimSpace(c.VolcengineTTS.VoiceType) != "") {
		c.TTS.Volcengine = c.VolcengineTTS
	}
}

type LLM struct {
	ApiKey  string `yaml:"apikey" mapstructure:"apikey"`
	BaseURL string `yaml:"baseurl" mapstructure:"baseurl"`
}

type AliOSS struct {
	AccessKeyID                    string `yaml:"accesskeyid" mapstructure:"accesskeyid"`
	AccessKeySecret                string `yaml:"accesskeysecret" mapstructure:"accesskeysecret"`
	BucketName                     string `yaml:"bucketname" mapstructure:"bucketname"`
	Endpoint                       string `yaml:"endpoint" mapstructure:"endpoint"`
	BucketRegion                   string `yaml:"bucketregion" mapstructure:"bucketregion"`
	AccessMode                     string `yaml:"accessmode" mapstructure:"accessmode"`
	SignedURLExpireSeconds         int64  `yaml:"signed_url_expire_seconds" mapstructure:"signed_url_expire_seconds"`
	ProviderURLExpireSeconds       int64  `yaml:"provider_url_expire_seconds" mapstructure:"provider_url_expire_seconds"`
	UserUploadRetentionDaysFree    int64  `yaml:"user_upload_retention_days_free" mapstructure:"user_upload_retention_days_free"`
	UserUploadRetentionDaysMember  int64  `yaml:"user_upload_retention_days_member" mapstructure:"user_upload_retention_days_member"`
	CleanupEnabled                 bool   `yaml:"cleanup_enabled" mapstructure:"cleanup_enabled"`
	CleanupIntervalSeconds         int64  `yaml:"cleanup_interval_seconds" mapstructure:"cleanup_interval_seconds"`
	CleanupBatchSize               int    `yaml:"cleanup_batch_size" mapstructure:"cleanup_batch_size"`
	ProviderAccessRetentionHours   int64  `yaml:"provider_access_retention_hours" mapstructure:"provider_access_retention_hours"`
	TaskIntermediateRetentionHours int64  `yaml:"task_intermediate_retention_hours" mapstructure:"task_intermediate_retention_hours"`
	CacheRetentionHours            int64  `yaml:"cache_retention_hours" mapstructure:"cache_retention_hours"`
	AuditRetentionHours            int64  `yaml:"audit_retention_hours" mapstructure:"audit_retention_hours"`
	DeleteRetryMax                 int    `yaml:"delete_retry_max" mapstructure:"delete_retry_max"`
	DeleteRetryDelaySeconds        int64  `yaml:"delete_retry_delay_seconds" mapstructure:"delete_retry_delay_seconds"`
}

type AliSTS struct {
	AccessKeyID     string `yaml:"accesskeyid" mapstructure:"accesskeyid"`
	AccessKeySecret string `yaml:"accesskeysecret" mapstructure:"accesskeysecret"`
	BucketName      string `yaml:"bucketname" mapstructure:"bucketname"`
	Endpoint        string `yaml:"endpoint" mapstructure:"endpoint"`
	BucketRegion    string `yaml:"bucketregion" mapstructure:"bucketregion"`
	RoleArn         string `yaml:"rolearn" mapstructure:"rolearn"`
	RegionID        string `yaml:"regionid" mapstructure:"regionid"`
}

type Volcengine struct {
	ApiKey                string `yaml:"apikey" mapstructure:"apikey"`
	Endpoint              string `yaml:"endpoint" mapstructure:"endpoint"`
	AccessKeyID           string `yaml:"accesskeyid" mapstructure:"accesskeyid"`
	AccessKeySecret       string `yaml:"accesskeysecret" mapstructure:"accesskeysecret"`
	ImageXRegion          string `yaml:"imagexregion" mapstructure:"imagexregion"`
	ImageXHost            string `yaml:"imagexhost" mapstructure:"imagexhost"`
	ImageXServiceID       string `yaml:"imagexserviceid" mapstructure:"imagexserviceid"`
	ImageXDomain          string `yaml:"imagexdomain" mapstructure:"imagexdomain"`
	ImageXTemplate        string `yaml:"imagextemplate" mapstructure:"imagextemplate"`
	ImageXModelAction     string `yaml:"imagexmodelaction" mapstructure:"imagexmodelaction"`
	ImageXModelVersion    string `yaml:"imagexmodelversion" mapstructure:"imagexmodelversion"`
	ImageXReqKey          string `yaml:"imagexreqkey" mapstructure:"imagexreqkey"`
	ImageXReqModelVersion string `yaml:"imagexreqmodelversion" mapstructure:"imagexreqmodelversion"`
}

type Keling struct {
	AccessKey string `yaml:"accesskey" mapstructure:"accesskey"`
	SecretKey string `yaml:"secretkey" mapstructure:"secretkey"`
	Endpoint  string `yaml:"endpoint" mapstructure:"endpoint"`
}

type AliyunAuth struct {
	RegionID     string `yaml:"regionid" mapstructure:"regionid"`
	AccessKeyID  string `yaml:"accesskeyid" mapstructure:"accesskeyid"`
	AccessSecret string `yaml:"accesssecret" mapstructure:"accesssecret"`
	SignName     string `yaml:"signname" mapstructure:"signname"`
	TemplateCode string `yaml:"templatecode" mapstructure:"templatecode"`
	SchemeName   string `yaml:"schemename" mapstructure:"schemename"`
	CodeLength   int    `yaml:"codelength" mapstructure:"codelength"`
	ValidTimeSec int    `yaml:"valid_time_sec" mapstructure:"valid_time_sec"`
	IntervalSec  int    `yaml:"interval_sec" mapstructure:"interval_sec"`
}

type AppStore struct {
	IssuerID              string `yaml:"issuerid" mapstructure:"issuerid"`
	KeyID                 string `yaml:"keyid" mapstructure:"keyid"`
	BundleID              string `yaml:"bundleid" mapstructure:"bundleid"`
	PrivateKey            string `yaml:"privatekey" mapstructure:"privatekey"`
	Environment           string `yaml:"environment" mapstructure:"environment"`
	RootCertPEM           string `yaml:"rootcertpem" mapstructure:"rootcertpem"`
	EnableSandboxFallback bool   `yaml:"enable_sandbox_fallback" mapstructure:"enable_sandbox_fallback"`
}

type AppleAuth struct {
	TeamID     string `yaml:"teamid" mapstructure:"teamid"`
	ClientID   string `yaml:"clientid" mapstructure:"clientid"`
	KeyID      string `yaml:"keyid" mapstructure:"keyid"`
	PrivateKey string `yaml:"privatekey" mapstructure:"privatekey"`
}

type TTS struct {
	Enabled    bool          `yaml:"enabled" mapstructure:"enabled"`
	Edge       TTSEdge       `yaml:"edge" mapstructure:"edge"`
	Volcengine VolcengineTTS `yaml:"volcengine" mapstructure:"volcengine"`
	Doubao     DoubaoTTS     `yaml:"doubao" mapstructure:"doubao"`
}

type TTSEdge struct {
	Command         string `mapstructure:"command"`           // 旧 CLI，保留兼容
	ServiceURL      string `mapstructure:"service_url"`       // 新增：HTTP provider URL
	SubmitTimeoutMs int    `mapstructure:"submit_timeout_ms"` // 默认 1000
	WaitTimeoutMs   int    `mapstructure:"wait_timeout_ms"`   // 默认 90000
	PollIntervalMs  int    `mapstructure:"poll_interval_ms"`  // 默认 1000
}

type FFmpegConfig struct {
	ServiceURL      string `mapstructure:"service_url"`
	SubmitTimeoutMs int    `mapstructure:"submit_timeout_ms"` // 默认 1000
	ProbeTimeoutMs  int    `mapstructure:"probe_timeout_ms"`  // 默认 30000
	WaitTimeoutMs   int    `mapstructure:"wait_timeout_ms"`   // 默认 300000
	PollIntervalMs  int    `mapstructure:"poll_interval_ms"`  // 默认 2000
}

// DoubaoTTS 豆包语音合成大模型 2.0 配置（适合电商带货口播，异步长文本接口）
type DoubaoTTS struct {
	AppID            string  `yaml:"appid" mapstructure:"appid"`
	AccessKey        string  `yaml:"accesskey" mapstructure:"accesskey"`
	Speaker          string  `yaml:"speaker" mapstructure:"speaker"`
	ResourceID       string  `yaml:"resourceid" mapstructure:"resourceid"`
	SubmitURL        string  `yaml:"submiturl" mapstructure:"submiturl"`
	QueryURL         string  `yaml:"queryurl" mapstructure:"queryurl"`
	AudioFormat      string  `yaml:"audioformat" mapstructure:"audioformat"`
	SampleRate       int     `yaml:"samplerate" mapstructure:"samplerate"`
	PricePer10KChars float64 `yaml:"price_per_10k_chars" mapstructure:"price_per_10k_chars"`
	TimeoutMS        int     `yaml:"timeout_ms" mapstructure:"timeout_ms"`
	WaitTimeoutMS    int     `yaml:"wait_timeout_ms" mapstructure:"wait_timeout_ms"`
	PollIntervalMS   int     `yaml:"poll_interval_ms" mapstructure:"poll_interval_ms"`
}

type VolcengineTTS struct {
	AppID            string  `yaml:"appid" mapstructure:"appid"`
	AccessToken      string  `yaml:"accesstoken" mapstructure:"accesstoken"`
	SecretKey        string  `yaml:"secretkey" mapstructure:"secretkey"`
	VoiceType        string  `yaml:"voicetype" mapstructure:"voicetype"`
	ResourceID       string  `yaml:"resourceid" mapstructure:"resourceid"`
	SubmitURL        string  `yaml:"submiturl" mapstructure:"submiturl"`
	QueryURL         string  `yaml:"queryurl" mapstructure:"queryurl"`
	AudioFormat      string  `yaml:"audioformat" mapstructure:"audioformat"`
	SampleRate       int     `yaml:"samplerate" mapstructure:"samplerate"`
	EnableSubtitle   int     `yaml:"enable_subtitle" mapstructure:"enable_subtitle"`
	PricePer10KChars float64 `yaml:"price_per_10k_chars" mapstructure:"price_per_10k_chars"`
	SubmitTimeoutMS  int     `yaml:"submit_timeout_ms" mapstructure:"submit_timeout_ms"`
	WaitTimeoutMS    int     `yaml:"wait_timeout_ms" mapstructure:"wait_timeout_ms"`
	PollIntervalMS   int     `yaml:"poll_interval_ms" mapstructure:"poll_interval_ms"`
}
