package contactmethod

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// SealExisting encrypts secret fields that are still plaintext, such as rows
// saved before sealing existed or while no key was configured. It is safe to
// run on every replica at startup: each row is replaced only if its config
// has not changed since it was read. It returns how many rows it sealed.
func SealExisting(ctx context.Context, s store.Store, enc Encrypter) (int, error) {
	if enc == nil {
		return 0, nil
	}
	cp, ok := s.(store.ContactMethodProvider)
	if !ok {
		return 0, nil
	}
	methods := cp.ContactMethods()
	types := make([]domain.ContactMethodType, 0, len(secretFields))
	for t := range secretFields {
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })

	sealed := 0
	for _, t := range types {
		list, err := methods.ListByType(ctx, t)
		if err != nil {
			return sealed, fmt.Errorf("list %s contact methods: %w", t, err)
		}
		for _, m := range list {
			cfg, err := SealConfig(m.Type, m.Config, enc)
			if err != nil {
				slog.Warn("contactmethod: seal existing config", "method_id", m.ID, "error", err)
				continue
			}
			if cfg == m.Config {
				continue
			}
			ok, err := methods.ReplaceConfig(ctx, m.ID, m.Config, cfg)
			if err != nil {
				return sealed, fmt.Errorf("seal contact method %s: %w", m.ID, err)
			}
			if ok {
				sealed++
			}
		}
	}
	return sealed, nil
}
