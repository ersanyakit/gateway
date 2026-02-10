package listeners

type Listener interface {
	Start() error
	Stop() error
}
