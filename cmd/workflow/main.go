package main

import (
	"log"

	"zeussdd/internal/workflow"
)

func main() {
	if err := workflow.Run(); err != nil {
		log.Fatal(err)
	}
}
