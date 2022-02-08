package watcher

const (
	DoGenerateUpdateEventOnStart    = true
	DoNotGenerateUpdateEventOnStart = false
)

type Settings struct {
	DoNotGenerateUpdateEventsOnStart bool
}
