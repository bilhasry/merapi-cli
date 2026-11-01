package cli

import (
	"fmt"
	"os"

	"github.com/bilhasry/merapi/internal/spec"
	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	var warningsOnly bool
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate the OpenAPI document and report documentation gaps",
		Long: `Validate the OpenAPI document against the OpenAPI 3.x meta-schema
(powered by libOpenAPI) and report documentation-quality warnings such as
missing summaries or descriptions.

Exit code is 1 when there are errors (or with --warnings-as-errors when there
are warnings), so it can gate CI pipelines.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, doc, err := mustDoc()
			if err != nil {
				return err
			}
			issues, err := doc.Validate()
			if err != nil {
				return err
			}
			var errs, warns []spec.Issue
			for _, issue := range issues {
				if issue.Severity == "error" {
					errs = append(errs, issue)
				} else {
					warns = append(warns, issue)
				}
			}
			if flagJSON {
				if err := printJSON(map[string]any{
					"valid":    len(errs) == 0,
					"errors":   orEmptyIssues(errs),
					"warnings": orEmptyIssues(warns),
					"file":     p.SpecPath(),
				}); err != nil {
					return err
				}
			} else {
				for _, issue := range errs {
					fmt.Fprintln(os.Stderr, paint(ansiRed, "error: ")+issue.String())
				}
				for _, issue := range warns {
					fmt.Fprintln(os.Stdout, paint(ansiYellow, "warning: ")+issue.String())
				}
				switch {
				case len(errs) > 0:
					errorf("%s: %d error(s), %d warning(s)", p.Config.Spec, len(errs), len(warns))
				case len(warns) > 0:
					warn("%s is valid (%d warning(s))", p.Config.Spec, len(warns))
				default:
					success("%s is a valid OpenAPI document, fully documented ✔", p.Config.Spec)
				}
			}
			if len(errs) > 0 || (warningsOnly && len(warns) > 0) {
				return errValidation
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&warningsOnly, "warnings-as-errors", false, "exit non-zero when warnings are present")
	return cmd
}

// errValidation triggers a non-zero exit without printing an error message.
var errValidation = validationError{}

type validationError struct{}

func (validationError) Error() string { return "validation failed" }

func orEmptyIssues(issues []spec.Issue) []spec.Issue {
	if issues == nil {
		return []spec.Issue{}
	}
	return issues
}
