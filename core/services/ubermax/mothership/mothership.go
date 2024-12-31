package mothership

import (
	"errors"
	"log/slog"
	"net/http"
	"pocker/core/ioc"
	"pocker/core/services/ubermax/mothership/mirror"
	"sync"

	"github.com/pluja/pocketbase"
)

type MothershipProviderConfig struct {
	Url      string
	Email    string
	Password string
	SseDebug bool
}

type MothershipProvider struct {
	config MothershipProviderConfig
	client *pocketbase.Client
	mirror *mirror.MirrorManager
}

func WithCloudflareRetryCondition() pocketbase.ClientOption {
	return pocketbase.WithStatusCodeRetryCondition(
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	)
}

var once sync.Once
var instance *MothershipProvider

func Mothership() *MothershipProvider {
	once.Do(func() {
		instance = New(MothershipProviderConfig{
			Url:      "https://pockethost-central.pocker.io",
			Email:    "ben@pockethost.io",
			Password: "",
		})
	})
	return instance
}

func New(config MothershipProviderConfig) *MothershipProvider {
	if config.Url == "" {
		panic("mothershipUrl is required")
	}
	if config.Email == "" {
		panic("mothershipEmail is required")
	}
	if config.Password == "" {
		panic("mothershipPassword is required")
	}

	baseUrl := func(path string) string {
		return config.Url + path
	}

	client := pocketbase.NewClient(baseUrl(""),
		pocketbase.WithAdminEmailPassword22(config.Email, config.Password),
		WithCloudflareRetryCondition(),
	)

	provider := MothershipProvider{
		config: config,
		client: client,
		mirror: mirror.NewMirrorManager(mirror.MirrorManagerConfig{
			Client:   client,
			SseDebug: config.SseDebug,
		}),
	}

	return &provider
}

func (p *MothershipProvider) ensureMothershipAuthenticated() error {
	for {
		err := p.client.Authorize()
		if err == nil {
			break
		}
		slog.Warn("Failed to authenticate mothership client. Retrying",
			"error", err)
	}
	slog.Debug("Mothership client authenticated")
	return nil
}

func (p *MothershipProvider) Start() {
	slog.Debug("Starting mothership provider")
	p.ensureMothershipAuthenticated()
	p.mirror.Start()
}

func (p *MothershipProvider) GetInstanceByHostHeader(host string) (ioc.IInstance, error) {
	return nil, errors.New("not implemented")
}

func (p *MothershipProvider) GetUserById(id string) (ioc.IUser, error) {
	return nil, errors.New("not implemented")
}

func (p *MothershipProvider) GetMachineById(id string) (ioc.IMachine, error) {
	return nil, errors.New("not implemented")
}
