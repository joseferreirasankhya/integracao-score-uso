package main

import (
	"log"

	"joseferreirasankhya/integracao-score-uso/internal/integration"
)

func main() {
	if err := integration.Run(); err != nil {
		log.Fatalln(err)
	}
}
