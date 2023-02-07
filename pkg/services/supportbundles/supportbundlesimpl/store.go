package supportbundlesimpl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/grafana/grafana/pkg/infra/kvstore"
	"github.com/grafana/grafana/pkg/services/supportbundles"
	"github.com/grafana/grafana/pkg/services/user"
)

const (
	defaultBundleExpiration = 72 * time.Hour // 72h
)

func newStore(kv kvstore.KVStore) *store {
	return &store{kv: kvstore.WithNamespace(kv, 0, "supportbundle")}
}

type store struct {
	kv *kvstore.NamespacedKVStore
}

type bundleStore interface {
	Create(ctx context.Context, usr *user.SignedInUser) (*supportbundles.Bundle, error)
	Get(ctx context.Context, uid string) (*supportbundles.Bundle, error)
	List() ([]supportbundles.Bundle, error)
	Remove(ctx context.Context, uid string) error
	Update(ctx context.Context, uid string, state supportbundles.State, tarBytes []byte) error
}

func (s *store) Create(ctx context.Context, usr *user.SignedInUser) (*supportbundles.Bundle, error) {
	// Maybe have the createBundleCounter here?
	fmt.Println("pkg/services/supportbundles/supportbundlesimpl/store.go:Create()")
	// fmt.Println(0)
	// bb, err := s.Get(ctx, "stats.bundles.count")
	// if err.Error() == "not found" {
	// 	fmt.Println(11)
	// 	// s.set(ctx, "stats.bundles.count")
	// }
	// if err != nil {
	// 	fmt.Println(1)
	// 	fmt.Println(err)
	// 	return nil, err
	// } else {
	// 	fmt.Println(2)
	// 	fmt.Println(bb)
	// }
	// fmt.Println(3)

	uid, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}

	bundle := supportbundles.Bundle{
		UID:       uid.String(),
		State:     supportbundles.StatePending,
		Creator:   usr.Login,
		CreatedAt: time.Now().Unix(),
		ExpiresAt: time.Now().Add(defaultBundleExpiration).Unix(),
	}

	// update stats.bundles.count

	if err := s.set(ctx, &bundle); err != nil {
		return nil, err
	}
	return &bundle, nil
}

func (s *store) Update(ctx context.Context, uid string, state supportbundles.State, tarBytes []byte) error {
	bundle, err := s.Get(ctx, uid)
	if err != nil {
		return err
	}

	bundle.State = state
	bundle.TarBytes = tarBytes

	return s.set(ctx, bundle)
}

func (s *store) set(ctx context.Context, bundle *supportbundles.Bundle) error {
	data, err := json.Marshal(&bundle)
	if err != nil {
		return err
	}
	return s.kv.Set(ctx, bundle.UID, string(data))
}

func (s *store) Get(ctx context.Context, uid string) (*supportbundles.Bundle, error) {
	data, ok, err := s.kv.Get(ctx, uid)
	if err != nil {
		return nil, err
	}
	if !ok {
		// FIXME: handle not found
		return nil, errors.New("not found")
	}
	var b supportbundles.Bundle
	if err := json.NewDecoder(strings.NewReader(data)).Decode(&b); err != nil {
		return nil, err
	}

	return &b, nil
}

func (s *store) Remove(ctx context.Context, uid string) error {
	return s.kv.Del(ctx, uid)
}

func (s *store) List() ([]supportbundles.Bundle, error) {
	data, err := s.kv.GetAll(context.Background())
	if err != nil {
		return nil, err
	}

	res := make([]supportbundles.Bundle, 0)
	for _, items := range data {
		for _, s := range items {
			var b supportbundles.Bundle
			if err := json.NewDecoder(strings.NewReader(s)).Decode(&b); err != nil {
				return nil, err
			}

			b.TarBytes = nil
			res = append(res, b)
		}
	}

	sort.Slice(res, func(i, j int) bool {
		return res[i].CreatedAt > res[j].CreatedAt
	})

	return res, nil
}
