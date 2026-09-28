package model

type Capability string

const (
	CapabilityCompletion = Capability("completion")
	CapabilityTools      = Capability("tools")
	CapabilityInsert     = Capability("insert")
	CapabilityVision     = Capability("vision")
	CapabilityEmbedding  = Capability("embedding")
	CapabilityThinking   = Capability("thinking")
	CapabilityImage      = Capability("image")
	CapabilityAudio      = Capability("audio")
	// CapabilityDocument is document input: a PDF or another file attachment
	// sent to the model. The Switch spells this modality "file" and hands it
	// out independently of image input, so it is its own capability rather
	// than something read off CapabilityVision.
	CapabilityDocument = Capability("document")
)

func (c Capability) String() string {
	return string(c)
}
