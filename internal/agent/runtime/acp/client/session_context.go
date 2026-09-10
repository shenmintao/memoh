package client

import (
	"errors"
	"fmt"
	"strings"

	"github.com/felinics/memoh/internal/workspace/bridge"
)

const HermesContainerHome = dataMountPath + "/.memoh-hermes"

type SessionContextInput struct {
	AgentID       string
	SetupMode     SetupMode
	Backend       string
	WorkspaceRoot string
	ProjectPath   string
}

type ResolvedSessionContext struct {
	AgentID       string
	SetupMode     SetupMode
	Backend       WorkspaceBackend
	WorkspaceRoot string
	ProjectPath   string
	CWD           string
	HermesHome    string
}

func ResolveSessionContext(input SessionContextInput) (ResolvedSessionContext, error) {
	var backend WorkspaceBackend
	var resolvedRoot, projectPath string
	switch strings.ToLower(strings.TrimSpace(input.Backend)) {
	case "", bridge.WorkspaceBackendContainer:
		backend = WorkspaceBackendContainer
		resolvedRoot = dataMountPath
		var err error
		projectPath, err = ResolvePathUnderVirtualRoot(resolvedRoot, input.ProjectPath)
		if err != nil {
			return ResolvedSessionContext{}, err
		}
	case bridge.WorkspaceBackendRemote:
		backend = WorkspaceBackendRemote
		resolvedRoot = strings.TrimSpace(input.WorkspaceRoot)
		projectPath = strings.TrimSpace(input.ProjectPath)
		if projectPath == "" {
			projectPath = resolvedRoot
		}
		if resolvedRoot == "" || projectPath == "" {
			return ResolvedSessionContext{}, errors.New("remote workspace paths are incomplete")
		}
	default:
		return ResolvedSessionContext{}, fmt.Errorf("unsupported workspace backend %q", input.Backend)
	}

	ctx := ResolvedSessionContext{
		AgentID:       strings.TrimSpace(input.AgentID),
		SetupMode:     normalizeSetupMode(input.SetupMode),
		Backend:       backend,
		WorkspaceRoot: resolvedRoot,
		ProjectPath:   projectPath,
		CWD:           projectPath,
	}
	if isHermesAgent(input.AgentID) && ctx.SetupMode != SetupModeSelf {
		ctx.HermesHome = HermesContainerHome
	}
	return ctx, nil
}

func resolveWorkspacePaths(info bridge.WorkspaceInfo, rawProjectPath string) (string, string, WorkspaceBackend, error) {
	ctx, err := ResolveSessionContext(SessionContextInput{
		Backend:       info.Backend,
		WorkspaceRoot: info.DefaultWorkDir,
		ProjectPath:   rawProjectPath,
	})
	if err != nil {
		return "", "", WorkspaceBackendContainer, err
	}
	return ctx.WorkspaceRoot, ctx.ProjectPath, ctx.Backend, nil
}
