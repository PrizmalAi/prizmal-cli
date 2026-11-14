package opencode

import launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"

// Inputs is what OpenCode declares for each Switch input type, and for a model
// the Switch said nothing about.
//
// OpenCode only forwards an attached image or PDF when the entry declares the
// matching input modality; otherwise it writes a refusal into the prompt and
// the model answers without the document. So an unknown model keeps the
// permissive list that entry has always declared, and a known text-only model
// gets the narrow one. Over-declaring is the safe direction for OpenCode.
var Inputs = launch.HarnessInputs{
	Names: map[string]string{
		launch.InputText:  "text",
		launch.InputImage: "image",
		launch.InputFile:  "pdf",
	},
	Unknown: []string{"text", "image", "pdf"},
}
