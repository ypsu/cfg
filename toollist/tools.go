package toollist

import (
	"context"
)

var Tools []Tool

type Tool struct {
	Fn   func(context.Context) error
	Desc string
}
