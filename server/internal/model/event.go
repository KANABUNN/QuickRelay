package model

import (
	"encoding/json"
	"time"
)

// Hypocenter retains the source's numeric values together with their qualification.
// Assumed values stay out of legacy top-level fields so older clients cannot
// accidentally present them without the accompanying note.
type Hypocenter struct {
	Status         string   `json:"status"`
	Note           string   `json:"note,omitempty"`
	OriginTime     *string  `json:"origin_time"`
	Epicenter      *string  `json:"epicenter"`
	Latitude       *float64 `json:"latitude"`
	Longitude      *float64 `json:"longitude"`
	DepthKM        *int     `json:"depth_km"`
	DepthCondition string   `json:"depth_condition,omitempty"`
	Magnitude      *float64 `json:"magnitude"`
}

func (h Hypocenter) Qualification() string {
	if h.Note != "" {
		return h.Note
	}
	if h.Status == "assumed" {
		return "仮定震源の参考値です。"
	}
	if h.Status == "low_accuracy" {
		return "精度の低い推定です。"
	}
	return ""
}

type Report struct {
	ID             string          `json:"id"`
	EventID        string          `json:"event_id"`
	ServerSequence int64           `json:"server_sequence"`
	MessageID      string          `json:"message_id"`
	Classification string          `json:"classification"`
	TelegramType   string          `json:"telegram_type"`
	EventType      string          `json:"event_type"`
	Serial         *int            `json:"revision"`
	Final          bool            `json:"is_final"`
	Cancelled      bool            `json:"is_cancelled"`
	Warning        bool            `json:"is_warning"`
	Title          string          `json:"title"`
	Body           string          `json:"body"`
	ReportedAt     time.Time       `json:"occurred_at"`
	ReceivedAt     time.Time       `json:"received_at"`
	OriginTime     *string         `json:"origin_time"`
	Epicenter      *string         `json:"epicenter"`
	Latitude       *float64        `json:"latitude"`
	Longitude      *float64        `json:"longitude"`
	DepthKM        *int            `json:"depth_km"`
	Magnitude      *float64        `json:"magnitude"`
	MaxIntensity   *string         `json:"max_intensity"`
	Hypocenter     *Hypocenter     `json:"hypocenter,omitempty"`
	Category       string          `json:"category,omitempty"`
	SourceEventID  string          `json:"source_event_id,omitempty"`
	InfoType       string          `json:"info_type,omitempty"`
	PressedAt      *time.Time      `json:"press_time,omitempty"`
	Bulletin       *Bulletin       `json:"bulletin,omitempty"`
	AffectedAreas  []AffectedArea  `json:"affected_areas,omitempty"`
	Raw            json.RawMessage `json:"-"`
}

func (r Report) IsEEW() bool {
	return r.Classification == "eew.forecast" || r.Classification == "eew.warning"
}
func (r Report) TTL() time.Duration {
	if r.IsEEW() {
		return 60 * time.Second
	}
	return 10 * time.Minute
}
func (r Report) NewerThan(p Report) bool {
	if !r.IsEEW() {
		// Ordinary bulletins are publications, not EEW report-number streams.
		// A later publication can follow a withdrawal; cancellation is not an all-clear.
		if !r.ReportedAt.Equal(p.ReportedAt) {
			return r.ReportedAt.After(p.ReportedAt)
		}
		if r.PressedAt != nil && p.PressedAt != nil && !r.PressedAt.Equal(*p.PressedAt) {
			return r.PressedAt.After(*p.PressedAt)
		}
		if r.Cancelled != p.Cancelled {
			return r.Cancelled
		}
		if r.InfoType != p.InfoType && r.InfoType == "訂正" {
			return true
		}
		if r.Bulletin != nil && p.Bulletin != nil && r.Bulletin.Document != nil && p.Bulletin.Document != nil {
			a, b := r.Bulletin.Document.Part, p.Bulletin.Document.Part
			if a != nil && b != nil {
				return *a > *b
			}
		}
		return false
	}
	if r.Serial != nil && p.Serial != nil {
		if *r.Serial != *p.Serial {
			return *r.Serial > *p.Serial && !(p.IsEEW() && (p.Cancelled || p.Final && !r.Cancelled))
		}
	}
	// Cancellation is terminal within its own telegram stream; it does not cancel other products.
	if p.Cancelled {
		return false
	}
	if r.Cancelled {
		return true
	}
	if p.Final && !r.Final {
		return false
	}
	if r.Final && !p.Final || r.Warning && !p.Warning {
		return true
	}
	return r.ReportedAt.After(p.ReportedAt)
}

type Event struct {
	ID           string      `json:"id"`
	Title        string      `json:"title,omitempty"`
	InfoType     string      `json:"info_type,omitempty"`
	Category     string      `json:"category"`
	EventType    string      `json:"event_type"`
	OriginTime   *string     `json:"origin_time"`
	Epicenter    *string     `json:"epicenter"`
	Latitude     *float64    `json:"latitude"`
	Longitude    *float64    `json:"longitude"`
	DepthKM      *int        `json:"depth_km"`
	Magnitude    *float64    `json:"magnitude"`
	MaxIntensity *string     `json:"max_intensity"`
	Hypocenter   *Hypocenter `json:"hypocenter,omitempty"`
	// Monotone server state version. Source report number is source_serial / report.revision.
	LatestRevision int64     `json:"latest_revision"`
	SourceSerial   *int      `json:"source_serial"`
	Classification string    `json:"classification"`
	TelegramType   string    `json:"telegram_type"`
	Final          bool      `json:"is_final"`
	Cancelled      bool      `json:"is_cancelled"`
	Warning        bool      `json:"is_warning"`
	LatestReportAt time.Time `json:"latest_report_at"`
}

func (r Report) Event() Event {
	return Event{ID: r.EventID, Category: r.CategoryName(), Title: r.Title, InfoType: r.InfoType, EventType: r.EventType, OriginTime: r.OriginTime,
		Epicenter: r.Epicenter, Latitude: r.Latitude, Longitude: r.Longitude, DepthKM: r.DepthKM, Magnitude: r.Magnitude,
		MaxIntensity: r.MaxIntensity, Hypocenter: r.Hypocenter, LatestRevision: r.ServerSequence, SourceSerial: r.Serial, Classification: r.Classification,
		TelegramType: r.TelegramType, Final: r.Final, Cancelled: r.Cancelled, Warning: r.Warning, LatestReportAt: r.ReportedAt}
}

type SyncItem struct {
	Report Report `json:"report"`
	Event  Event  `json:"event"`
}
type SyncPage struct {
	OK         bool       `json:"ok"`
	Items      []SyncItem `json:"items"`
	Next       int64      `json:"next_after_sequence"`
	HasMore    bool       `json:"has_more"`
	Latest     int64      `json:"latest_committed_sequence"`
	ServerTime string     `json:"server_time"`
}
