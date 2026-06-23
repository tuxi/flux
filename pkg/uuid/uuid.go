package uuid

import (
	"strings"

	"github.com/bwmarrin/snowflake"
	"github.com/google/uuid"
)

type SnowNode struct {
	Node *snowflake.Node
}

// GenUUID 生成一个随机的唯一ID
func GenUUID() string {
	return uuid.NewString()
}

// GenUUID16 截取uuid前16位
func GenUUID16() string {
	uuidStr := uuid.NewString()
	uuidStr = strings.ReplaceAll(uuidStr, "-", "")
	return uuidStr[0:16]
}

func NewNode(i int64) *SnowNode {
	node, err := snowflake.NewNode(i)
	
	if err != nil {
		panic(err)
	}
	return &SnowNode{
		Node: node,
	}
}

func (sn *SnowNode) GenSnowID() int64 {
	id := sn.Node.Generate().Int64()
	return id
}

func (sn *SnowNode) GenSnowStr() string {
	id := sn.Node.Generate().String()
	return id
}
