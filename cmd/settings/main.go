package main

import (
	"log"

	"zeussdd/internal/settings"
)

func main() {
	if err := settings.Run(); err != nil {
		log.Fatal(err)
	}
}
