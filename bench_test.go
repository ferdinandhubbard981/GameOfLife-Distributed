package main

import (
	"fmt"
	"testing"

	"uk.ac.bris.cs/gameoflife/gol"
)

func BenchmarkLocal(b *testing.B) {
	turns := 100
	threadConfs := []int{1}
	imageConfs := []int{512}

	for _, imageSize := range imageConfs {
		for _, threads := range threadConfs {
			p := gol.Params{
				Turns:       turns,
				Threads:     threads,
				ImageWidth:  imageSize,
				ImageHeight: imageSize,
			}
			name := fmt.Sprintf("size=%dx%d_threads=%d_turns=%d_", imageSize, imageSize, threads, turns)
			b.Run(name, func(b *testing.B) {
				benchmark(b, p)
			})
		}
	}

}

func benchmark(b *testing.B, p gol.Params) {
	for i := 0; i < b.N; i++ {
		events := make(chan gol.Event)
		go gol.Run(p, events, nil)

		for event := range events {
			switch event.(type) {
			case gol.FinalTurnComplete:
				break
			}
		}
	}

}
