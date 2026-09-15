package main

import (
	"backend-challenge-go/internal"
	"go.uber.org/fx"
)

func main() {
	fx.New(internal.Module).Run()
}
