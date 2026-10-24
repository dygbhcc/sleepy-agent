package main

import "path/filepath"

// mockLabel is the label both sources give a scripted mock.
const mockLabel = "scripted mock"

// outDir keeps mock output out of the folder that holds real measurements,
// so a run without an API key can never be mistaken for one.
func outDir(label string) string {
	if label == mockLabel {
		return filepath.Join("out", "scriptstats-mock")
	}
	return filepath.Join("out", "scriptstats")
}
