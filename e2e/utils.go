// Copyright 2023 The etcd Authors
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
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	clientv2 "go.etcd.io/etcd/client/v2"
	"go.etcd.io/etcd/tests/v3/framework/e2e"
	"go.etcd.io/etcd/tests/v3/integration"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/pkg/v3/stringutil"
)

func newClient(t *testing.T, entpoints []string, connType e2e.ClientConnType, isAutoTLS bool) *clientv3.Client {
	tlscfg, err := tlsInfo(t, connType, isAutoTLS)
	if err != nil {
		t.Fatal(err)
	}
	ccfg := clientv3.Config{
		Endpoints:   entpoints,
		DialTimeout: 5 * time.Second,
		DialOptions: []grpc.DialOption{grpc.WithBlock()},
	}
	if tlscfg != nil {
		tls, err := tlscfg.ClientConfig()
		if err != nil {
			t.Fatal(err)
		}
		ccfg.TLS = tls
	}
	c, err := clientv3.New(ccfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.Close()
	})
	return c
}

func newClientV2(t *testing.T, endpoints []string, connType e2e.ClientConnType, isAutoTLS bool) (clientv2.Client, error) {
	tls, err := tlsInfo(t, connType, isAutoTLS)
	if err != nil {
		t.Fatal(err)
	}
	cfg := clientv2.Config{
		Endpoints: endpoints,
	}
	if tls != nil {
		cfg.Transport, err = transport.NewTransport(*tls, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
	}
	return clientv2.New(cfg)
}

func tlsInfo(t testing.TB, connType e2e.ClientConnType, isAutoTLS bool) (*transport.TLSInfo, error) {
	switch connType {
	case e2e.ClientNonTLS, e2e.ClientTLSAndNonTLS:
		return nil, nil
	case e2e.ClientTLS:
		if isAutoTLS {
			tls, err := transport.SelfCert(zap.NewNop(), t.TempDir(), []string{"localhost"}, 1)
			if err != nil {
				return nil, fmt.Errorf("failed to generate cert: %s", err)
			}
			return &tls, nil
		}
		return &integration.TestTLSInfo, nil
	default:
		return nil, fmt.Errorf("config %v not supported", connType)
	}
}

func fillEtcdWithData(ctx context.Context, c *clientv3.Client, dbSize int) error {
	g := errgroup.Group{}
	concurrency := 10
	keyCount := 100
	keysPerRoutine := keyCount / concurrency
	valueSize := dbSize / keyCount
	for i := 0; i < concurrency; i++ {
		i := i
		g.Go(func() error {
			for j := 0; j < keysPerRoutine; j++ {
				_, err := c.Put(ctx, fmt.Sprintf("%d", i*keysPerRoutine+j), stringutil.RandString(uint(valueSize)))
				if err != nil {
					return err
				}
			}
			return nil
		})
	}
	return g.Wait()
}

func getMemberIdByName(ctx context.Context, c *e2e.Etcdctl, name string) (id uint64, found bool, err error) {
	resp, err := c.MemberList()
	if err != nil {
		return 0, false, err
	}
	for _, member := range resp.Members {
		if name == member.Name {
			return member.ID, true, nil
		}
	}
	return 0, false, nil
}

// Different implementations here since 3.5 e2e test framework does not have "initial-cluster-state" as a default argument
// Append new flag if not exist, otherwise replace the value
func patchArgs(args []string, flag, newValue string) []string {
	for i, arg := range args {
		if strings.Contains(arg, flag) {
			args[i] = fmt.Sprintf("--%s=%s", flag, newValue)
			return args
		}
	}
	args = append(args, fmt.Sprintf("--%s=%s", flag, newValue))
	return args
}
