//go:build !unix

package config

import "errors"

func readCredentials(string) ([]byte, error) {
	return nil, errors.New("secure credential storage is supported only on Unix platforms")
}

func writeCredentials(string, []byte) error {
	return errors.New("secure credential storage is supported only on Unix platforms")
}

func replaceCredentialsWithBackup(string, []byte, []byte) error {
	return errors.New("secure credential storage is supported only on Unix platforms")
}
