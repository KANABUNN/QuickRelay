// Package ingest owns the durable receipt -> normalization -> report/outbox boundary.
package ingest

import (
	"context"
	"errors"
	"time"

	"quakerelay/server/internal/dmdata"
	"quakerelay/server/internal/model"
	"quakerelay/server/internal/store"
)

type Result struct {
	ReceiptID, State                       string
	Report                                 model.Report
	Ingest                                 store.IngestResult
	ArchiveTime, NormalizeTime, CommitTime time.Duration
	// Rejected messages are archived, without making reception fail/reconnect.
	Rejection error
}

type Processor struct {
	Store      *store.Store
	Historical bool
}

func (p Processor) Process(ctx context.Context, frame []byte, received time.Time) (Result, error) {
	var out Result
	started := time.Now()
	id, err := p.Store.ArchiveFrame(ctx, frame, received)
	out.ArchiveTime = time.Since(started)
	if err != nil {
		return out, errors.New("telegram archive persistence failed")
	}
	out.ReceiptID = id
	started = time.Now()
	out.Report, err = dmdata.Normalize(frame, received)
	out.NormalizeTime = time.Since(started)
	switch {
	case errors.Is(err, dmdata.ErrIgnored):
		out.State = "ignored"
	case err != nil:
		out.State, out.Rejection = "rejected", err
	default:
		out.State = "normalized"
	}
	started = time.Now()
	if out.State == "normalized" {
		if p.Historical {
			out.Ingest, err = p.Store.IngestHistorical(ctx, out.Report)
		} else {
			out.Ingest, err = p.Store.Ingest(ctx, out.Report)
		}
		if err != nil {
			return out, errors.New("ingestion persistence failed")
		}
	}
	if err = p.Store.CompleteFrame(ctx, id, out.State, out.Report.ID); err != nil {
		return out, errors.New("telegram receipt completion failed")
	}
	out.CommitTime = time.Since(started)
	return out, nil
}
