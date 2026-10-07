package model

// Bulletin keeps human-readable source sections separate from a notification's summary.
type Bulletin struct {
	Headline string            `json:"headline,omitempty"`
	Sections []BulletinSection `json:"sections,omitempty"`
	Document *SourceDocument   `json:"document,omitempty"`
}
type BulletinSection struct {
	Title string        `json:"title"`
	Text  string        `json:"text,omitempty"`
	Rows  []BulletinRow `json:"rows,omitempty"`
}
type BulletinRow struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
type SourceDocument struct {
	Format      string `json:"format"`
	ByteCount   int    `json:"byte_count"`
	SHA256      string `json:"sha256"`
	Designation string `json:"designation,omitempty"`
	Part        *int   `json:"part,omitempty"`
	Complete    bool   `json:"complete"`
}

func (r Report) CategoryName() string {
	if r.Category != "" {
		return r.Category
	}
	return "earthquake"
}
func (r Report) Attention() bool {
	return r.IsEEW() || r.EventType == "tsunami_warning" || r.EventType == "nankai_info" && r.Warning
}

// Raw non-XML products do not carry a trustworthy operational/test flag.
// Keep every received part available in authenticated history without alerting.
func (r Report) PushEligible() bool {
	return r.TelegramType != "IXAC41" && r.TelegramType != "WEPA60"
}
