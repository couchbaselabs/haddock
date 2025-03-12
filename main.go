package main

import (
	"cod/logger"
	"cod/server"
)

func main() {
	// Initialize the logger
	logger.Init()

	// Start the server
	ser := server.NewServer()
	ser.Start()
}
