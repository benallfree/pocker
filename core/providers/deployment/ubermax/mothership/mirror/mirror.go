package mirror

import (
	"log/slog"

	"pocker/core/providers/deployment/ubermax/mothership/models"

	"github.com/pluja/pocketbase"
)

type ICacheable interface {
	GetId() string
}

type MirrorData struct {
	Users     []models.User     `json:"users"`
	Instances []models.Instance `json:"instances"`
}

type MirrorManager struct {
	instances *MirrorCache[*models.Instance]
	users     *MirrorCache[*models.User]
	machines  *MirrorCache[*models.Machine]
	config    MirrorManagerConfig
}

type MirrorManagerConfig struct {
	Client   *pocketbase.Client
	SseDebug bool
}

func NewMirrorManager(config MirrorManagerConfig) *MirrorManager {
	return &MirrorManager{
		instances: newMirrorCache(MirrorCacheConfig[*models.Instance]{
			Client:         config.Client,
			Debug:          config.SseDebug,
			CollectionName: "instances",
			Fields:         []string{"id", "cname", "subdomain", "supension", "uid"},
			Factory:        models.NewInstance,
		}),
		users: newMirrorCache(MirrorCacheConfig[*models.User]{
			Client:         config.Client,
			Debug:          config.SseDebug,
			CollectionName: "users",
			Fields:         []string{"id", "email", "verified", "suspension", "double_verified"},
			Factory:        models.NewUser,
		}),
		machines: newMirrorCache(MirrorCacheConfig[*models.Machine]{
			Client:         config.Client,
			Debug:          config.SseDebug,
			CollectionName: "machines",
			Fields:         []string{"id", "name", "uuid", "privateUrl"},
			Factory:        models.NewMachine,
		}),
		config: config,
	}
}

func (p *MirrorManager) Start() {
	slog.Info("Starting mirror manager")

	p.instances.StartMirroring()
	p.users.StartMirroring()
	p.machines.StartMirroring()
}
