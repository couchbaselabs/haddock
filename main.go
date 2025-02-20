package main

import (

    "cod/server"
)



func main() {
    ser := server.NewServer()
    ser.Start()
}