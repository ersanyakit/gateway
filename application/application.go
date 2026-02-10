package application

import (
	blockchain "core/blockchain"

	socketio "github.com/vchitai/go-socket.io/v4"
	gorm "gorm.io/gorm"
)

type App struct {
	DB           *gorm.DB
	SocketServer *socketio.Server
	Blockchains  *blockchain.ChainFactory
}
