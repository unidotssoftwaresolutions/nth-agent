package ui

import "os"

// enableEscapes readies a terminal to read escape codes, and answers whether it will.
//
// On every system this agent is built for today a terminal reads them as it is, so there is nothing to do. A system whose
// console must first be switched into reading them gets its switch HERE, in this one function and nowhere else: this file
// then takes a build tag naming the systems that need nothing, and a sibling file for that system flips the console's mode
// and answers false when it cannot, which turns colour off for that output and leaves every word of it in place.
func enableEscapes(*os.File) bool { return true }
