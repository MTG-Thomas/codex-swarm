package main

import (
	"github.com/MTG-Thomas/codex-swarm/internal/bifrost"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

type bifrostStore struct {
	store *store.JSONStore
}

func (s bifrostStore) SaveBifrostChangeset(record bifrost.Record) error {
	return s.store.SaveBifrostChangeset(toStoreChangeset(record))
}

func (s bifrostStore) GetBifrostChangeset(id string) (bifrost.Record, error) {
	record, err := s.store.GetBifrostChangeset(id)
	if err != nil {
		return bifrost.Record{}, err
	}
	return fromStoreChangeset(record), nil
}

func (s bifrostStore) ListBifrostChangesets() ([]bifrost.Record, error) {
	records, err := s.store.ListBifrostChangesets()
	if err != nil {
		return nil, err
	}
	result := make([]bifrost.Record, 0, len(records))
	for _, record := range records {
		result = append(result, fromStoreChangeset(record))
	}
	return result, nil
}

func toStoreChangeset(record bifrost.Record) store.BifrostChangeset {
	return store.BifrostChangeset{
		ID: record.ID, WorkerID: record.WorkerID, Target: record.Target,
		Scope: record.Scope, BaseRevision: record.BaseRevision,
		RemoteChangesetID: record.RemoteChangesetID, State: record.State,
		Validation: record.Validation, CommitSHA: record.CommitSHA,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func fromStoreChangeset(record store.BifrostChangeset) bifrost.Record {
	return bifrost.Record{
		ID: record.ID, WorkerID: record.WorkerID, Target: record.Target,
		Scope: record.Scope, BaseRevision: record.BaseRevision,
		RemoteChangesetID: record.RemoteChangesetID, State: record.State,
		Validation: record.Validation, CommitSHA: record.CommitSHA,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}
