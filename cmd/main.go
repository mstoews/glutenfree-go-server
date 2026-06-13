package main

import (
	application "github.com/mstoews/glutenfree-server/app"
	"github.com/mstoews/glutenfree-server/runtime"
	"github.com/rs/zerolog/log"
)

func main() {
	a := application.Application{}
	if err := a.LoadConfigurations(); err != nil {
		log.Fatal().Err(err).Msg("failed to load configurations")
	}
	if err := runtime.Start(&a); err != nil {
		log.Fatal().Err(err).Msg("failed to start the application")
	}
}
