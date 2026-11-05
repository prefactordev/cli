package cli

import (
	"errors"
	"os"
	"strings"

	"charm.land/huh/v2"
)

// Choice is one entry in a picker.
type Choice struct {
	Label string
	Value string
}

// Picker asks the user to choose one entry, or to type a value. It is only
// used in interactive mode.
type Picker interface {
	// Pick shows title, an optional note under it, and the choices.
	Pick(title, note string, choices []Choice) (string, error)
	// Input asks for a line of text, which must not be blank when
	// required. The answer is trimmed.
	Input(title string, required bool) (string, error)
}

// errPickCancelled is returned when the user leaves a picker without
// choosing.
var errPickCancelled = errors.New("Cancelled.")

// huhPicker is a list drawn on stderr, so stdout stays parseable. Typing /
// searches it.
type huhPicker struct{}

func (huhPicker) Pick(title, note string, choices []Choice) (string, error) {
	options := make([]huh.Option[string], len(choices))
	for i, c := range choices {
		options[i] = huh.NewOption(c.Label, c.Value)
	}

	var value string
	field := huh.NewSelect[string]().
		Title(title).
		Description(note).
		Options(options...).
		Height(min(len(choices)+2, 15)).
		Value(&value)

	err := huh.NewForm(huh.NewGroup(field)).WithOutput(os.Stderr).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return "", errPickCancelled
	}
	return value, err
}

func (huhPicker) Input(title string, required bool) (string, error) {
	var value string
	field := huh.NewInput().Title(title).Value(&value)
	if required {
		field.Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("Required.")
			}
			return nil
		})
	}

	err := huh.NewForm(huh.NewGroup(field)).WithOutput(os.Stderr).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return "", errPickCancelled
	}
	return strings.TrimSpace(value), err
}
