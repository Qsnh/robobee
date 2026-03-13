package main

import (
	"log"
	"os"

	"github.com/robobee/core/internal/config"
)

func main() {
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	app, err := buildApp(cfg)
	if err != nil {
		log.Fatalf("failed to build app: %v", err)
	}

	app.Run()
}
