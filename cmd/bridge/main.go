package main

import (
	"log"

	"zeussdd/internal/bridge"
)

func main() {
	if err := bridge.Run(); err != nil {
		log.Fatal(err)
	}
}
