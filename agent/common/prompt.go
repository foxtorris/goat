package common

import (
	"github.com/cloudwego/eino/schema"
)

type AgentUserInput struct {
	Text   string
	Images []*schema.ContentBlock
}

func (u AgentUserInput) String() string {
	return u.Text
}
