//go:build !darwin || !cgo || server

package desktop

func platformLanguage() string { return "" }
