package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/timonwong/jiro/internal/apperr"
	"github.com/timonwong/jiro/internal/markup"
	"github.com/timonwong/jiro/internal/output"
)

const jfmConversionCommandAnnotation = "jiro.io/jfm-conversion"

type jfmConverter func(context.Context, string) (string, []markup.ConversionWarning, error)

type jfmConversionSpec struct {
	name        string
	short       string
	direction   jfmConversionDirection
	resultField string
	convert     jfmConverter
}

func (a *app) jfmCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "jfm",
		Short: "Convert between Jiro Flavored Markdown and Jira Markup",
		Long:  "Convert documents between Jiro Flavored Markdown and Jira Markup without a Jira connection.",
	}
	command.AddCommand(
		a.jfmConversionCommand(jfmConversionSpec{
			name: "to-jira", short: "Convert Jiro Flavored Markdown to Jira Markup",
			direction: jfmToJiraDirection, resultField: "jiraMarkup",
			convert: func(ctx context.Context, source string) (string, []markup.ConversionWarning, error) {
				result, err := markup.FromJFM(ctx, source)
				return result.Markup, result.Warnings, err
			},
		}),
		a.jfmConversionCommand(jfmConversionSpec{
			name: "from-jira", short: "Convert Jira Markup to Jiro Flavored Markdown",
			direction: jiraToJFMDirection, resultField: "jfm",
			convert: func(ctx context.Context, source string) (string, []markup.ConversionWarning, error) {
				result, err := markup.ToJFM(ctx, source)
				return result.Markdown, result.Warnings, err
			},
		}),
	)
	return command
}

func (a *app) jfmConversionCommand(spec jfmConversionSpec) *cobra.Command {
	return &cobra.Command{
		Use:   spec.name + " [FILE|-]",
		Short: spec.short,
		Long:  spec.short + ". Read FILE when provided, or read standard input when FILE is omitted or -.",
		Args:  atMostArgs(1),
		Annotations: map[string]string{
			jfmConversionCommandAnnotation: "true",
		},
		RunE: func(command *cobra.Command, args []string) error {
			path := "-"
			if len(args) == 1 {
				path = args[0]
			}
			source, err := a.readJFMInput(command.Context(), path)
			if err != nil {
				return err
			}
			if err := command.Context().Err(); err != nil {
				return err
			}
			converted, warnings, err := spec.convert(command.Context(), string(source))
			if err != nil {
				return err
			}
			if err := command.Context().Err(); err != nil {
				return err
			}
			return a.renderJFMConversion(converted, spec.resultField, spec.direction, warnings)
		},
	}
}

func atMostArgs(count int) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) > count {
			return apperr.New(apperr.KindInvalidInput, fmt.Sprintf("expected at most %d argument(s), got %d", count, len(args)))
		}
		return nil
	}
}

func (a *app) readJFMInput(ctx context.Context, path string) ([]byte, error) {
	var reader io.Reader = a.stdin
	closer, _ := a.stdin.(io.Closer)
	var file *os.File
	if path != "-" {
		opened, err := openFileWithContext(ctx, path)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil, err
			}
			return nil, apperr.Wrap(apperr.KindInvalidInput, err, "read JFM input %q", path)
		}
		file = opened
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return nil, apperr.Wrap(apperr.KindInvalidInput, err, "inspect JFM input %q", path)
		}
		if info.IsDir() {
			return nil, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("JFM input %q is a directory", path))
		}
		reader, closer = file, file
	}
	data, err := readAllWithContext(ctx, reader, closer)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, apperr.Wrap(apperr.KindInvalidInput, err, "read JFM input")
	}
	return data, nil
}

func (a *app) renderJFMConversion(
	converted, resultField string,
	direction jfmConversionDirection,
	warnings []markup.ConversionWarning,
) error {
	renderer, err := a.renderer()
	if err != nil {
		return err
	}
	renderer = renderer.WithWarnings(jfmConversionWarnings(direction, warnings, nil)...)
	if renderer.Format == output.FormatText {
		err = renderer.Success([]byte(converted))
	} else {
		err = renderer.Success(map[string]string{resultField: converted})
	}
	if isBrokenPipe(err) {
		return nil
	}
	return err
}

func isJFMConversionCommand(command *cobra.Command) bool {
	return command != nil && command.Annotations[jfmConversionCommandAnnotation] == "true"
}
