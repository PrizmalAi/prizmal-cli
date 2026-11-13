package kilo

import launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"

// Inputs is what Kilo declares for each Switch input type, and for a model the
// Switch said nothing about.
//
// Kilo withholds an attachment whose type the entry does not list, so
// declaring pdf for a model that never reported one would offer a document the
// model may refuse. An unknown model gets text and image, the list its
// launcher has always declared, and no pdf.
var Inputs = launch.HarnessInputs{
	Names: map[string]string{
		launch.InputText:  "text",
		launch.InputImage: "image",
		launch.InputFile:  "pdf",
	},
	Unknown: []string{"text", "image"},
}
