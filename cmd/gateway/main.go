package main

import (
	"log"

	"zeussdd/internal/gateway"
)

func main() {
	if err := gateway.Run(); err != nil {
		log.Fatal(err)
	}
}
