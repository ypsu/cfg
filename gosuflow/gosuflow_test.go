package gosuflow

import (
	"context"
	"os"
	"testing"

	"github.com/ypsu/efftesting"
)

type HelloWorld struct {
	HelloSection struct{}
	name         string

	WorldSection struct{}
	greeting     string

	FinalizeSection struct{}
	done            bool
}

func (hw *HelloWorld) Hello(context.Context) error {
	efftesting.Must(!hw.done)
	hw.name = "alice"
	return nil
}

func (hw *HelloWorld) World(context.Context) error {
	efftesting.Must(!hw.done)
	hw.greeting = "Hello " + hw.name + "!"
	return nil
}

func (hw *HelloWorld) Finalize(context.Context) error {
	hw.done = true
	return nil
}

func TestRun(t *testing.T) {
	et := efftesting.New(t)

	hw := &HelloWorld{}
	efftesting.Must(Run(t.Context(), hw))
	et.Expect("Greeting", hw.greeting, "Hello alice!")
	et.Expect("Done", hw.done, "true")

	et.Expect("BadUsage", Run(t.Context(), HelloWorld{}), "gosuflow.MissingMethod method=Hello")
}

func TestMain(m *testing.M) {
	os.Exit(efftesting.Main(m))
}
