package engine

import "github.com/K-RED90/gidm/internal/config"

type Engine struct {
	cfg   config.Download
	store Store
}

func New(cfg config.Download, store Store) *Engine {
	return &Engine{cfg: cfg, store: store}
}

func (e *Engine) Ready() bool {
	return e.store != nil && e.cfg.MaxConcurrent > 0
}
