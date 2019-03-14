// +build linux

package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/liamg/aminal/config"
	"github.com/liamg/aminal/gui"
	"github.com/liamg/aminal/terminal"

	"github.com/carlogit/phash"
)

var termRef *terminal.Terminal
var guiRef *gui.GUI

func terminate(msg string) int {
	defer fmt.Print(msg)
	return 1
}

func sleep(seconds ...int) {
	count := 2
	if len(seconds) > 0 {
		count = seconds[0]
	}
	time.Sleep(time.Duration(count) * time.Second)
}

func hash(path string) string {
	image, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer image.Close()
	imageHash, err := phash.GetHash(image)
	if err != nil {
		panic(err)
	}
	return imageHash
}

func compareImages(expected string, actual string) {
	template := hash(expected)
	screen := hash(actual)
	distance := phash.GetDistance(template, screen)
	if distance != 0 {
		os.Exit(terminate(fmt.Sprintf("Screenshot \"%s\" doesn't match expected image \"%s\". Distance of hashes difference: %d\n",
			actual, expected, distance)))
	}
}

func send(terminal *terminal.Terminal, cmd string) {
	terminal.Write([]byte(cmd))
}

func enter(terminal *terminal.Terminal) {
	terminal.Write([]byte("\n"))
}

func validateScreen(img string) {
	guiRef.Screenshot(img)
	compareImages(strings.Join([]string{"vttest/", img}, ""), img)

	enter(termRef)
	sleep()
}

func TestMain(m *testing.M) {
	flag.Parse()

	go m.Run()

	for f := range tests {
		f()
	}
}

var tests = make(chan func())

func runMain(f func()) {
	complete := make(chan bool, 1)
	tests <- func() {
		f()
		complete <- true
	}
	<-complete
}

func TestCursorMovement(t *testing.T) {
	runMain(func() {

		testFunc := func(term *terminal.Terminal, g *gui.GUI) {
			termRef = term
			guiRef = g

			sleep()
			send(term, "vttest\n")
			sleep()
			send(term, "1\n")
			sleep()

			term.Lock()
			compareResult := term.ActiveBuffer().CompareViewLines("vttest/test-cursor-movement-1")
			term.Unlock()

			if compareResult == false {
				os.Exit(terminate(fmt.Sprintf("ActiveBuffer doesn't match vttest template vttest/test-cursor-movement-1")))
			}

			validateScreen("test-cursor-movement-1.png")
			validateScreen("test-cursor-movement-2.png")
			validateScreen("test-cursor-movement-3.png")
			validateScreen("test-cursor-movement-4.png")
			validateScreen("test-cursor-movement-5.png")
			validateScreen("test-cursor-movement-6.png")

			g.Close()
		}

		initialize(testFunc, testConfig())
	})
}

func TestScreenFeatures(t *testing.T) {
	runMain(func() {

		testFunc := func(term *terminal.Terminal, g *gui.GUI) {
			termRef = term
			guiRef = g

			sleep()
			send(term, "vttest\n")
			sleep()
			send(term, "2\n")
			sleep()

			validateScreen("test-screen-features-1.png")
			validateScreen("test-screen-features-2.png")
			validateScreen("test-screen-features-3.png")
			validateScreen("test-screen-features-4.png")
			validateScreen("test-screen-features-5.png")
			validateScreen("test-screen-features-6.png")
			validateScreen("test-screen-features-7.png")
			validateScreen("test-screen-features-8.png")
			validateScreen("test-screen-features-9.png")
			validateScreen("test-screen-features-10.png")
			validateScreen("test-screen-features-11.png")
			validateScreen("test-screen-features-12.png")
			validateScreen("test-screen-features-13.png")
			validateScreen("test-screen-features-14.png")
			validateScreen("test-screen-features-15.png")

			g.Close()
		}

		initialize(testFunc, testConfig())
	})
}

func TestSixel(t *testing.T) {
	runMain(func() {

		testFunc := func(term *terminal.Terminal, g *gui.GUI) {
			termRef = term
			guiRef = g

			sleep()
			send(term, "export PS1='> '\n")
			sleep()
			send(term, "clear\n")
			sleep()
			send(term, "cat example.sixel\n")
			sleep(4)

			guiRef.Screenshot("test-sixel.png")
			validateScreen("test-sixel.png")

			g.Close()
		}

		initialize(testFunc, testConfig())
	})
}

// Last Test should terminate main goroutine since it's infinity looped to execute others GUI tests in main goroutine
func TestExit(t *testing.T) {
	os.Exit(0)
}

func testConfig() *config.Config {
	c := config.DefaultConfig()

	// Use a vanilla shell on POSIX to help ensure consistency.
	if runtime.GOOS != "windows" {
		c.Shell = "/bin/sh"
	}

	return c
}
