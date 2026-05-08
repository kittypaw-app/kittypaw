package engine

import (
	"fmt"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

func setConversationStaff(baseDir string, st *store.Store, staffRef string) (string, error) {
	if st == nil {
		return "", fmt.Errorf("store is required")
	}
	base, err := core.ResolveBaseDir(baseDir)
	if err != nil {
		return "", err
	}
	id, ok, err := core.ResolveStaffReference(base, staffRef)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("staff %q를 찾지 못했습니다", staffRef)
	}
	if err := st.SetConversationStaff(id); err != nil {
		return "", err
	}
	return id, nil
}
