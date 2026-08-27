// Copyright 2026 FanYaNan. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"io"

	"github.com/druidcaesa/neuralgate/pkg/machineid"
)

// machineIDCommand 打印本机机器码(供客户取码送签);返回进程退出码
func machineIDCommand(w io.Writer) int {
	fp, err := machineid.Fingerprint()
	if err != nil {
		fmt.Fprintln(w, "获取机器码失败:", err)
		return 1
	}
	fmt.Fprintln(w, fp)
	return 0
}
