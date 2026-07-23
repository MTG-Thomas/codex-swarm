package protocol

import "github.com/MTG-Thomas/codex-swarm/internal/store"

type CodexTask = store.CodexTask
type CodexTaskObservation = store.CodexTaskObservation
type CodexTaskClassification = store.CodexTaskClassification
type CodexTaskIngestRequest = store.CodexTaskIngestRequest
type CodexTaskIngestResponse = store.CodexTaskIngestResult
type CodexTaskListResponse = store.CodexTaskPage
type CodexTaskStatusResponse = store.CodexTaskStats
