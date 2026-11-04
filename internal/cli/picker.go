package cli

import (
	"errors"
	"os"

	"github.com/charmbracelet/huh"
)

// Choice is one entry in a picker.
type Choice struct {
	Label string
	Value string
}

// Picker asks the user to choose one entry. It is only used in interactive
// mode.
type Picker interface {
	Pick(title string, choices []Choice) (string, error)
}

// errPickCancelled is returned when the user leaves a picker without
// choosing.
var errPickCancelled = errors.New("Cancelled.")

// huhPicker is a list drawn on stderr, so stdout stays parseable. Typing /
// searches it.
type huhPicker struct{}

func (huhPicker) Pick(title string, choices []Choice) (string, error) {
	options := make([]huh.Option[string], len(choices))
	for i, c := range choices {
		options[i] = huh.NewOption(c.Label, c.Value)
	}

	var value string
	field := huh.NewSelect[string]().
		Title(title).
		Options(options...).
		Height(min(len(choices)+2, 15)).
		Value(&value)

	err := huh.NewForm(huh.NewGroup(field)).WithOutput(os.Stderr).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return "", errPickCancelled
	}
	return value, err
}
