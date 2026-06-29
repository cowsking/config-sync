// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package metrics

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegisterOTelExporter(t *testing.T) {
	origDisable := os.Getenv("DISABLE_MONITORING")
	defer func() {
		if origDisable != "" {
			os.Setenv("DISABLE_MONITORING", origDisable)
		} else {
			os.Unsetenv("DISABLE_MONITORING")
		}
	}()

	testCases := map[string]struct {
		disableMonitoring string
		expectNilExporter bool
	}{
		"disabled": {
			disableMonitoring: "true",
			expectNilExporter: true,
		},
		"default (enabled)": {
			disableMonitoring: "",
			expectNilExporter: false,
		},
		"explicitly enabled": {
			disableMonitoring: "false",
			expectNilExporter: false,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			if tc.disableMonitoring != "" {
				t.Setenv("DISABLE_MONITORING", tc.disableMonitoring)
			} else {
				os.Unsetenv("DISABLE_MONITORING")
			}

			exporter, err := RegisterOTelExporter(context.Background(), "test-container")

			if tc.expectNilExporter {
				assert.NoError(t, err)
				assert.Nil(t, exporter)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, exporter)
				if exporter != nil {
					_ = exporter.Shutdown(context.Background())
				}
			}
		})
	}
}
