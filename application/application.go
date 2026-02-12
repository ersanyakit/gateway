package application

import (
	"core/api/routes"

	socketio "github.com/vchitai/go-socket.io/v4"
	gorm "gorm.io/gorm"
)

type App struct {
	DB           *gorm.DB
	Router       *routes.Router
	SocketServer *socketio.Server
}

var (
	CORE *App
)
