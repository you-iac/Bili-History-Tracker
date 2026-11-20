//go:build !windows

// protect_other.go 在尚未接入系统凭据库的平台禁用凭据落盘；扫描状态仍持久化。
package localstate

import "errors"

const persistentCookie = false

func protect([]byte) ([]byte, error)   { return nil, errors.New("系统凭据存储尚未接入") }
func unprotect([]byte) ([]byte, error) { return nil, errors.New("系统凭据存储尚未接入") }
