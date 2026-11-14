package pi

import launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"

// Inputs is what Pi declares for each Switch input type, and for a model the
// Switch said nothing about. Pi has no document modality, so a document
// capability reaches it and has nowhere to land. It declares text alone for an
// unknown model, which is what it declares for a text-only one.
var Inputs = launch.HarnessInputs{
	Names: map[string]string{
		launch.InputText:  "text",
		launch.InputImage: "image",
	},
	Unknown: []string{"text"},
}
