package terminal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LayoutResult holds the result of creating an auto layout.
type LayoutResult struct {
	Panes        map[string]string // provider -> pane_id
	RootPaneID   string
	NeedsAttach  bool
	CreatedPanes []string
}

// CreateAutoLayout creates a tmux split layout for 1-4 providers.
//
// Layout rules:
//   - 1 AI: no split
//   - 2 AI: left/right
//   - 3 AI: left 1 + right top/bottom 2
//   - 4 AI: 2x2 grid
func CreateAutoLayout(
	providers []string,
	cwd string,
	rootPaneID string,
	tmuxSessionName string,
	percent int,
	setMarkers bool,
	markerPrefix string,
) (*LayoutResult, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("providers must not be empty")
	}
	if len(providers) > 4 {
		return nil, fmt.Errorf("providers max is 4 for auto layout")
	}

	backend := NewTmuxBackend("")
	var created []string
	panes := make(map[string]string)
	needsAttach := false

	// Resolve/allocate root pane.
	root := strings.TrimSpace(rootPaneID)
	if root == "" {
		var err error
		root, err = backend.GetCurrentPaneID()
		if err != nil {
			// Daemon/outside tmux: create a detached session as a container.
			sessionName := strings.TrimSpace(tmuxSessionName)
			if sessionName == "" {
				sessionName = fmt.Sprintf("curdx-%s-%d-%d", filepath.Base(cwd), int(time.Now().Unix())%100000, os.Getpid())
			}
			if !backend.IsAlive(sessionName) {
				if _, err := backend.TmuxRun([]string{"new-session", "-d", "-s", sessionName, "-c", cwd}, true, false, nil, 0); err != nil {
					return nil, err
				}
			}
			result, err := backend.TmuxRun([]string{"list-panes", "-t", sessionName, "-F", "#{pane_id}"}, true, true, nil, 0)
			if err != nil {
				return nil, err
			}
			stdout := strings.TrimSpace(result.Stdout)
			if stdout != "" {
				root = strings.TrimSpace(strings.Split(stdout, "\n")[0])
			}
			if root == "" || !strings.HasPrefix(root, "%") {
				return nil, fmt.Errorf("failed to allocate tmux root pane")
			}
			created = append(created, root)
			needsAttach = strings.TrimSpace(os.Getenv("TMUX")) == ""
		}
	}

	panes[providers[0]] = root

	mark := func(provider string, paneID string) {
		if !setMarkers {
			return
		}
		backend.SetPaneTitle(paneID, fmt.Sprintf("%s-%s", markerPrefix, provider))
	}

	mark(providers[0], root)

	if len(providers) == 1 {
		return &LayoutResult{
			Panes:        panes,
			RootPaneID:   root,
			NeedsAttach:  needsAttach,
			CreatedPanes: created,
		}, nil
	}

	pct := percent
	if pct < 1 {
		pct = 1
	}
	if pct > 99 {
		pct = 99
	}

	if len(providers) == 2 {
		right, err := backend.SplitPane(root, "right", pct)
		if err != nil {
			return nil, err
		}
		created = append(created, right)
		panes[providers[1]] = right
		mark(providers[1], right)
		return &LayoutResult{
			Panes:        panes,
			RootPaneID:   root,
			NeedsAttach:  needsAttach,
			CreatedPanes: created,
		}, nil
	}

	if len(providers) == 3 {
		rightTop, err := backend.SplitPane(root, "right", pct)
		if err != nil {
			return nil, err
		}
		created = append(created, rightTop)
		rightBottom, err := backend.SplitPane(rightTop, "bottom", pct)
		if err != nil {
			return nil, err
		}
		created = append(created, rightBottom)
		panes[providers[1]] = rightTop
		panes[providers[2]] = rightBottom
		mark(providers[1], rightTop)
		mark(providers[2], rightBottom)
		return &LayoutResult{
			Panes:        panes,
			RootPaneID:   root,
			NeedsAttach:  needsAttach,
			CreatedPanes: created,
		}, nil
	}

	// 4 providers: 2x2 grid
	rightTop, err := backend.SplitPane(root, "right", pct)
	if err != nil {
		return nil, err
	}
	created = append(created, rightTop)
	leftBottom, err := backend.SplitPane(root, "bottom", pct)
	if err != nil {
		return nil, err
	}
	created = append(created, leftBottom)
	rightBottom, err := backend.SplitPane(rightTop, "bottom", pct)
	if err != nil {
		return nil, err
	}
	created = append(created, rightBottom)

	panes[providers[1]] = rightTop
	panes[providers[2]] = leftBottom
	panes[providers[3]] = rightBottom
	mark(providers[1], rightTop)
	mark(providers[2], leftBottom)
	mark(providers[3], rightBottom)

	return &LayoutResult{
		Panes:        panes,
		RootPaneID:   root,
		NeedsAttach:  needsAttach,
		CreatedPanes: created,
	}, nil
}
