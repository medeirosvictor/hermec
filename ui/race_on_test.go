//go:build race

package ui

// raceBuild is true under -race, where the runtime adds allocations that make
// AllocsPerRun assertions unreliable.
const raceBuild = true
