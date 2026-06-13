package app

import "github.com/mstoews/glutenfree-server/util"

// Application holds process-wide configuration loaded before the server starts.
type Application struct {
	Config util.Config
}

// LoadConfigurations reads app.env (and environment overrides) into Config.
func (a *Application) LoadConfigurations() error {
	config, err := util.LoadConfig(".")
	if err != nil {
		return err
	}
	a.Config = config
	return nil
}
