/*
Copyright 2025 Psiphon Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package udsipc

import (
	"os"
	"runtime"
)

// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// LocalPath returns a local path that can be used for Unix-domain
// protocol testing.
// This function is copied from golang.org/x/net/nettest package.
func LocalPath() (string, error) {
	dir := ""
	if runtime.GOOS == "darwin" {
		dir = "/tmp"
	}
	f, err := os.CreateTemp(dir, "go-nettest")
	if err != nil {
		return "", err
	}
	path := f.Name()
	f.Close()
	os.Remove(path)
	return path, nil
}
