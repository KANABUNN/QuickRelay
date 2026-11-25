package eewbridge

import (
	"strconv"

	"quakerelay/server/internal/eew"
)

// WSS messages, schemaVersion 1 (contracts/eew-wss.schema.json). Each is one
// UTF-8 text frame holding one JSON object. Positions are decimal strings.
const SchemaVersion = 1

type Cursor struct {
	StreamID string `json:"streamId"`
	Epoch    string `json:"epoch"`
	Seq      string `json:"seq"`
}

type SubscribeMessage struct {
	SchemaVersion int     `json:"schemaVersion"`
	Type          string  `json:"type"`
	Environment   string  `json:"environment"`
	Resume        *Cursor `json:"resume"`
}

type SyncBeginMessage struct {
	SchemaVersion int    `json:"schemaVersion"`
	Type          string `json:"type"`
	StreamID      string `json:"streamId"`
	Epoch         string `json:"epoch"`
	Mode          string `json:"mode"`
	AsOfSeq       string `json:"asOfSeq"`
}

type StateMessage struct {
	SchemaVersion int       `json:"schemaVersion"`
	Type          string    `json:"type"`
	StreamID      string    `json:"streamId"`
	Epoch         string    `json:"epoch"`
	Seq           string    `json:"seq"`
	Delivery      string    `json:"delivery"`
	Cause         string    `json:"cause"`
	CommittedAt   string    `json:"committedAt"`
	ChangedSeries string    `json:"changedSeries"`
	Event         eew.Event `json:"event"`
}

type SnapshotMessage struct {
	SchemaVersion int         `json:"schemaVersion"`
	Type          string      `json:"type"`
	StreamID      string      `json:"streamId"`
	Epoch         string      `json:"epoch"`
	AsOfSeq       string      `json:"asOfSeq"`
	Part          int         `json:"part"`
	Parts         int         `json:"parts"`
	Events        []eew.Event `json:"events"`
}

type SyncCompleteMessage struct {
	SchemaVersion int    `json:"schemaVersion"`
	Type          string `json:"type"`
	Cursor        Cursor `json:"cursor"`
}

type HealthMessage struct {
	SchemaVersion int    `json:"schemaVersion"`
	Type          string `json:"type"`
	ServerTime    string `json:"serverTime"`
	Upstream      string `json:"upstream"`
	Capture       string `json:"capture"`
}

func seqText(n int64) string { return strconv.FormatInt(n, 10) }

func stateMessage(stream Stream, r LogRecord, delivery string) StateMessage {
	return StateMessage{SchemaVersion: SchemaVersion, Type: "eew.state", StreamID: stream.ID, Epoch: stream.Epoch, Seq: seqText(r.Seq),
		Delivery: delivery, Cause: r.Cause, CommittedAt: r.CommittedAt.Format("2006-01-02T15:04:05.000Z"), ChangedSeries: r.ChangedSeries, Event: r.Event}
}
