package cli

import (
	"errors"
	"os"
	"slices"
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
	// MultiPick shows title, an optional note and the choices, with the
	// choices in selected ticked, and returns the values of those ticked.
	// At least one must be ticked.
	MultiPick(title, note string, choices []Choice, selected []string) ([]string, error)
	// Secret asks for a line of text that is not shown as it is typed. It
	// must not be blank. The answer is trimmed.
	Secret(title string) (string, error)
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
		field.Validate(requireText)
	}

	err := huh.NewForm(huh.NewGroup(field)).WithOutput(os.Stderr).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return "", errPickCancelled
	}
	return strings.TrimSpace(value), err
}

func (huhPicker) MultiPick(title, note string, choices []Choice, selected []string) ([]string, error) {
	options := make([]huh.Option[string], len(choices))
	for i, c := range choices {
		options[i] = huh.NewOption(c.Label, c.Value)
	}

	value := slices.Clone(selected)
	field := huh.NewMultiSelect[string]().
		Title(title).
		Description(note).
		Options(options...).
		Height(min(len(choices)+3, 15)).
		Validate(func(values []string) error {
			if len(values) == 0 {
				return errors.New("Choose at least one.")
			}
			return nil
		}).
		Value(&value)

	err := huh.NewForm(huh.NewGroup(field)).WithOutput(os.Stderr).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return nil, errPickCancelled
	}
	return value, err
}

func (huhPicker) Secret(title string) (string, error) {
	var value string
	field := huh.NewInput().Title(title).EchoMode(huh.EchoModePassword).Validate(requireText).Value(&value)

	err := huh.NewForm(huh.NewGroup(field)).WithOutput(os.Stderr).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return "", errPickCancelled
	}
	return strings.TrimSpace(value), err
}

func requireText(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("Required.")
	}
	return nil
}
