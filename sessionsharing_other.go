//go:build !windows

package main

func SetupSessionSharing(instanceName string) {}

func RepairSessionSharing(instanceName string) {}

func CleanupOfficialJunctions() {}
