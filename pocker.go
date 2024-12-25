package pocker

import "pocker/core/proxy"

type PockerConfig struct {
	ProxyConfig proxy.ProxyConfig
}

type Pocker struct {
	PockerConfig
}

func NewPocker(cfg PockerConfig) *Pocker {
	return &Pocker{
		PockerConfig: cfg,
	}
}

func (p *Pocker) Start() {
	server := proxy.NewProxy(p.ProxyConfig)
	server.Start()
}
