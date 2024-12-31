package port_range

import (
	"fmt"
	"log"
	"pocker/core/ioc"
	"pocker/core/syncx"
)

var _ ioc.IPortService = (*FixedPortRangeProvider)(nil)

type FixedPortRangeProvider struct {
	ports syncx.Pool[int]
}

type FixedPortRangeProviderConfig struct {
	PortRangeStart int
	PortRangeEnd   int
}

func New(config FixedPortRangeProviderConfig) ioc.IPortService {
	portStart := config.PortRangeStart
	if portStart == 0 {
		portStart = 10000
	}
	portEnd := config.PortRangeEnd
	if portEnd == 0 {
		portEnd = 12000
	}
	maxPortAssigned := portStart

	provider := FixedPortRangeProvider{
		ports: syncx.Pool[int]{
			New: func() int {
				maxPortAssigned++
				log.Printf("Allocating port: %d", maxPortAssigned)
				if maxPortAssigned > portEnd {
					log.Printf("No more ports available")
					return 0
				}
				return maxPortAssigned
			},
		},
	}

	return &provider
}

func (sm *FixedPortRangeProvider) AllocatePort() (int, error) {
	port := sm.ports.Get()
	if port == 0 {
		return 0, fmt.Errorf("no more ports available")
	}
	return port, nil
}

func (sm *FixedPortRangeProvider) Start() {

}
