// Copyright 2016 The etcd Authors
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

package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.etcd.io/etcd/api/v3/version"
	"go.etcd.io/etcd/client/pkg/v3/fileutil"
	"go.etcd.io/etcd/client/pkg/v3/testutil"
	"go.etcd.io/etcd/pkg/v3/flags"
)

func TestCtlV3Version(t *testing.T) { testCtl(t, versionTest) }

func TestClusterVersion(t *testing.T) {
	BeforeTest(t)

	tests := []struct {
		name         string
		rollingStart bool
	}{
		{
			name:         "When start servers at the same time",
			rollingStart: false,
		},
		{
			name:         "When start servers one by one",
			rollingStart: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary := binDir + "/etcd"
			if !fileutil.Exist(binary) {
				t.Skipf("%q does not exist", binary)
			}
			BeforeTest(t)
			cfg := newConfigNoTLS()
			cfg.execPath = binary
			cfg.snapshotCount = 3
			cfg.baseScheme = "unix" // to avoid port conflict
			cfg.rollingStart = tt.rollingStart

			epc, err := newEtcdProcessCluster(t, cfg)
			if err != nil {
				t.Fatalf("could not start etcd process cluster (%v)", err)
			}
			defer func() {
				if errC := epc.Close(); errC != nil {
					t.Fatalf("error closing etcd processes (%v)", errC)
				}
			}()

			ctx := ctlCtx{
				t:   t,
				cfg: *cfg,
				epc: epc,
			}
			cv := version.Cluster(version.Version)
			clusterVersionTest(ctx, `"etcdcluster":"`+cv)
		})
	}
}

func versionTest(cx ctlCtx) {
	if err := ctlV3Version(cx); err != nil {
		cx.t.Fatalf("versionTest ctlV3Version error (%v)", err)
	}
}

func clusterVersionTest(cx ctlCtx, expected string) {
	var err error
	for i := 0; i < 35; i++ {
		if err = cURLGet(cx.epc, cURLReq{endpoint: "/version", expected: expected}); err != nil {
			cx.t.Logf("#%d: v3 is not ready yet (%v)", i, err)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		break
	}
	if err != nil {
		cx.t.Fatalf("failed cluster version test expected %v got (%v)", expected, err)
	}
}

func ctlV3Version(cx ctlCtx) error {
	cmdArgs := append(cx.PrefixArgs(), "version")
	return spawnWithExpectWithEnv(cmdArgs, cx.envMap, version.Version)
}

// TestCtlV3DialWithHTTPScheme ensures that client handles endpoints with HTTPS scheme.
func TestCtlV3DialWithHTTPScheme(t *testing.T) {
	testCtl(t, dialWithSchemeTest, withCfg(*newConfigClientTLS()))
}

func dialWithSchemeTest(cx ctlCtx) {
	cmdArgs := append(cx.prefixArgs(cx.epc.EndpointsV3()), "put", "foo", "bar")
	if err := spawnWithExpectWithEnv(cmdArgs, cx.envMap, "OK"); err != nil {
		cx.t.Fatal(err)
	}
}

type ctlCtx struct {
	t                 *testing.T
	apiPrefix         string
	cfg               etcdProcessClusterConfig
	quotaBackendBytes int64
	corruptFunc       func(string) error
	noStrictReconfig  bool

	epc *etcdProcessCluster

	envMap map[string]string

	dialTimeout time.Duration
	testTimeout time.Duration

	quorum      bool // if true, set up 3-node cluster and linearizable read
	interactive bool

	user string
	pass string

	initialCorruptCheck bool

	// for compaction
	compactPhysical bool

	// to run etcdutl instead of etcdctl for suitable commands.
	etcdutl bool

	// dir that was used during the test
	dataDir string
}

type ctlOption func(*ctlCtx)

func (cx *ctlCtx) applyOpts(opts []ctlOption) {
	for _, opt := range opts {
		opt(cx)
	}
	cx.initialCorruptCheck = true
}

func withCfg(cfg etcdProcessClusterConfig) ctlOption {
	return func(cx *ctlCtx) { cx.cfg = cfg }
}

func withDialTimeout(timeout time.Duration) ctlOption {
	return func(cx *ctlCtx) { cx.dialTimeout = timeout }
}

func withTestTimeout(timeout time.Duration) ctlOption {
	return func(cx *ctlCtx) { cx.testTimeout = timeout }
}

func withQuorum() ctlOption {
	return func(cx *ctlCtx) { cx.quorum = true }
}

func withInteractive() ctlOption {
	return func(cx *ctlCtx) { cx.interactive = true }
}

func withQuota(b int64) ctlOption {
	return func(cx *ctlCtx) { cx.quotaBackendBytes = b }
}

func withCompactPhysical() ctlOption {
	return func(cx *ctlCtx) { cx.compactPhysical = true }
}

func withInitialCorruptCheck() ctlOption {
	return func(cx *ctlCtx) { cx.initialCorruptCheck = true }
}

func withCorruptFunc(f func(string) error) ctlOption {
	return func(cx *ctlCtx) { cx.corruptFunc = f }
}

func withNoStrictReconfig() ctlOption {
	return func(cx *ctlCtx) { cx.noStrictReconfig = true }
}

func withApiPrefix(p string) ctlOption {
	return func(cx *ctlCtx) { cx.apiPrefix = p }
}

func withFlagByEnv() ctlOption {
	return func(cx *ctlCtx) { cx.envMap = make(map[string]string) }
}

func withEtcdutl() ctlOption {
	return func(cx *ctlCtx) { cx.etcdutl = true }
}

// This function must be called after the `withCfg`, otherwise its value
// may be overwritten by `withCfg`.
func withMaxConcurrentStreams(streams uint32) ctlOption {
	return func(cx *ctlCtx) {
		cx.cfg.MaxConcurrentStreams = streams
	}
}

// This function must be called after the `withCfg`, otherwise its value
// may be overwritten by `withCfg`.
func withSnapshotCount(snapshotCount int) ctlOption {
	return func(cx *ctlCtx) {
		cx.cfg.snapshotCount = snapshotCount
	}
}

func withLogLevel(logLevel string) ctlOption {
	return func(cx *ctlCtx) {
		cx.cfg.logLevel = logLevel
	}
}

func testCtl(t *testing.T, testFunc func(ctlCtx), opts ...ctlOption) {
	testCtlWithOffline(t, testFunc, nil, opts...)
}

func getDefaultCtlCtx(t *testing.T) ctlCtx {
	return ctlCtx{
		t:           t,
		cfg:         *newConfigAutoTLS(),
		dialTimeout: 7 * time.Second,
	}
}

func testCtlWithOffline(t *testing.T, testFunc func(ctlCtx), testOfflineFunc func(ctlCtx), opts ...ctlOption) {
	BeforeTest(t)

	ret := getDefaultCtlCtx(t)
	ret.applyOpts(opts)

	if !ret.quorum {
		ret.cfg = *configStandalone(ret.cfg)
	}
	if ret.quotaBackendBytes > 0 {
		ret.cfg.quotaBackendBytes = ret.quotaBackendBytes
	}
	ret.cfg.noStrictReconfig = ret.noStrictReconfig
	if ret.initialCorruptCheck {
		ret.cfg.initialCorruptCheck = ret.initialCorruptCheck
	}
	if testOfflineFunc != nil {
		ret.cfg.keepDataDir = true
	}

	epc, err := newEtcdProcessCluster(t, &ret.cfg)
	if err != nil {
		t.Fatalf("could not start etcd process cluster (%v)", err)
	}
	ret.epc = epc
	ret.dataDir = epc.procs[0].Config().dataDirPath

	defer func() {
		if ret.envMap != nil {
			for k := range ret.envMap {
				os.Unsetenv(k)
			}
			ret.envMap = make(map[string]string)
		}
		if ret.epc != nil {
			if errC := ret.epc.Close(); errC != nil {
				t.Fatalf("error closing etcd processes (%v)", errC)
			}
		}
	}()

	donec := make(chan struct{})
	go func() {
		defer close(donec)
		testFunc(ret)
		t.Log("---testFunc logic DONE")
	}()

	timeout := ret.getTestTimeout()

	select {
	case <-time.After(timeout):
		testutil.FatalStack(t, fmt.Sprintf("test timed out after %v", timeout))
	case <-donec:
	}

	t.Log("closing test cluster...")
	assert.NoError(t, epc.Close())
	epc = nil
	t.Log("closed test cluster...")

	if testOfflineFunc != nil {
		testOfflineFunc(ret)
	}
}

func (cx *ctlCtx) getTestTimeout() time.Duration {
	timeout := cx.testTimeout
	if timeout == 0 {
		timeout = 2*cx.dialTimeout + time.Second
		if cx.dialTimeout == 0 {
			timeout = 30 * time.Second
		}
	}
	return timeout
}

func (cx *ctlCtx) prefixArgs(eps []string) []string {
	fmap := make(map[string]string)
	fmap["endpoints"] = strings.Join(eps, ",")
	fmap["dial-timeout"] = cx.dialTimeout.String()
	if cx.epc.cfg.clientTLS == clientTLS {
		if cx.epc.cfg.isClientAutoTLS {
			fmap["insecure-transport"] = "false"
			fmap["insecure-skip-tls-verify"] = "true"
		} else if cx.epc.cfg.isClientCRL {
			fmap["cacert"] = caPath
			fmap["cert"] = revokedCertPath
			fmap["key"] = revokedPrivateKeyPath
		} else {
			fmap["cacert"] = caPath
			fmap["cert"] = certPath
			fmap["key"] = privateKeyPath
		}
	}
	if cx.user != "" {
		fmap["user"] = cx.user + ":" + cx.pass
	}

	useEnv := cx.envMap != nil

	cmdArgs := []string{ctlBinPath + "3"}
	for k, v := range fmap {
		if useEnv {
			ek := flags.FlagToEnv("ETCDCTL", k)
			cx.envMap[ek] = v
		} else {
			cmdArgs = append(cmdArgs, fmt.Sprintf("--%s=%s", k, v))
		}
	}
	return cmdArgs
}

// PrefixArgs prefixes etcdctl command.
// Make sure to unset environment variables after tests.
func (cx *ctlCtx) PrefixArgs() []string {
	return cx.prefixArgs(cx.epc.EndpointsV3())
}

// PrefixArgsUtl returns prefix of the command that is either etcdctl or etcdutl
// depending on cx configuration.
// Please not thet 'utl' compatible commands does not consume --endpoints flag.
func (cx *ctlCtx) PrefixArgsUtl() []string {
	if cx.etcdutl {
		return []string{utlBinPath}
	}
	return []string{ctlBinPath}
}

func isGRPCTimedout(err error) bool {
	return strings.Contains(err.Error(), "grpc: timed out trying to connect")
}

func (cx *ctlCtx) memberToRemove() (ep string, memberID string, clusterID string) {
	n1 := cx.cfg.clusterSize
	if n1 < 2 {
		cx.t.Fatalf("%d-node is too small to test 'member remove'", n1)
	}

	resp, err := getMemberList(*cx)
	if err != nil {
		cx.t.Fatal(err)
	}
	if n1 != len(resp.Members) {
		cx.t.Fatalf("expected %d, got %d", n1, len(resp.Members))
	}

	ep = resp.Members[0].ClientURLs[0]
	clusterID = fmt.Sprintf("%x", resp.Header.ClusterId)
	memberID = fmt.Sprintf("%x", resp.Members[1].ID)

	return ep, memberID, clusterID
}
