package definition

// BuildDefinitionFromDB WorkflowNodeDefinitionModel 转换 WorkflowDefinition
//func BuildDefinitionFromDB(
//	Name string,
//	outputJSON []byte,
//	nodes []*entity.WorkflowNodeDefinitionModel,
//	edges []*entity.WorkflowEdgeDefinitionModel,
//) (*WorkflowDefinition, error) {
//
//	def := &WorkflowDefinition{
//		Name: Name,
//	}
//	var outputMapping map[string]string
//	if err := json.Unmarshal(outputJSON, &outputMapping); err == nil {
//		def.Output = outputMapping
//	}
//	nodeMap := map[string]*NodeDefinition{}
//
//	// 1️⃣ 创建节点
//	for _, n := range nodes {
//
//		name := n.Name
//
//		node, err := parseNodeDefinition(*n)
//		if err != nil {
//			return nil, err
//		}
//		nodeMap[name] = node
//	}
//
//	// 2️⃣ 解析边
//	for _, e := range edges {
//
//		def.Edges = append(def.Edges, EdgeDefinition{
//			From:      e.FormNode,
//			To:        e.ToNode,
//			Condition: e.ConditionExpr,
//			CaseKey:   e.CaseValue,
//			Priority:  e.Priority,
//			Label:     e.Label,
//			Type:      EdgeType(e.Type),
//		})
//	}
//
//	// 3️⃣ 生成 list
//	for _, v := range nodeMap {
//		def.Nodes = append(def.Nodes, *v)
//	}
//
//	return def, nil
//}
//
//func parseNodeDefinition(m entity.WorkflowNodeDefinitionModel) (*NodeDefinition, error) {
//
//	var config map[string]any
//	if len(m.ConfigJSON) > 0 {
//		err := json.Unmarshal(m.ConfigJSON, &config)
//		if err != nil {
//			log.Println("BuildDefinitionFromDB 解析ConfigJSON失败:%V", err)
//			return nil, err
//		}
//	}
//
//	inputMapping := map[string]string{}
//
//	if v, ok := config["input_mapping"]; ok {
//
//		for k, val := range v.(map[string]any) {
//			inputMapping[k] = val.(string)
//		}
//
//		delete(config, "input_mapping")
//	}
//
//	return &NodeDefinition{
//		Name:         m.Name,
//		Type:         NodeType(m.Type),
//		Weight:       m.Weight,
//		Config:       config,
//		InputMapping: inputMapping,
//	}, nil
//}
