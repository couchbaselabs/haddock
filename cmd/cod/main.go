package main

import (
	"cod/internal/logger"
	"cod/internal/server"
)

func main() {
	// Initialize the logger
	logger.Init()

	// Start the server
	ser := server.NewServer()
	ser.Start()
}
