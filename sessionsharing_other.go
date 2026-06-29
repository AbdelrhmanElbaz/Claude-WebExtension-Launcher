//go:build !windows

package main

func SetupSessionSharing(instanceName string) {}

func CleanupOfficialJunctions() {}
