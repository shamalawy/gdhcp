package common

import (
	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/cli/client"
)

// AddListFlags registers the common list flags (--limit, --offset, --sort,
// --search) shared by every `list` subcommand (docs/CONTRACTS.md Part 6
// "Common Query Parameters").
func AddListFlags(cmd *cobra.Command) {
	cmd.Flags().Int("limit", 0, "Maximum number of results (server default 100, max 1000)")
	cmd.Flags().Int("offset", 0, "Number of results to skip")
	cmd.Flags().String("sort", "", "Sort fields, comma-separated; prefix a field with - for descending (e.g. -created_at,name)")
	cmd.Flags().String("search", "", "Substring search across the resource's searchable fields")
}

// ListParams reads the flags AddListFlags registered on cmd into a
// client.ListParams. Extra is left nil; callers add resource-specific
// filters to the returned value's Extra field or via the list method's own
// parameters.
func ListParams(cmd *cobra.Command) client.ListParams {
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")
	sort, _ := cmd.Flags().GetString("sort")
	search, _ := cmd.Flags().GetString("search")
	return client.ListParams{Limit: limit, Offset: offset, Sort: sort, Search: search}
}

// StringFlagPtr returns a *string for use in a partial-update request body:
// nil if the flag was never set on the command line (leave field
// unchanged), a pointer to its value otherwise (including an explicit
// empty string, which clears the field).
func StringFlagPtr(cmd *cobra.Command, name string) *string {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	v, _ := cmd.Flags().GetString(name)
	return &v
}

// BoolFlagPtr returns a *bool for a partial-update request body: nil if the
// flag was never set, a pointer to its value otherwise.
func BoolFlagPtr(cmd *cobra.Command, name string) *bool {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	v, _ := cmd.Flags().GetBool(name)
	return &v
}

// IntFlagPtr returns an *int for a partial-update request body: nil if the
// flag was never set, a pointer to its value otherwise.
func IntFlagPtr(cmd *cobra.Command, name string) *int {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	v, _ := cmd.Flags().GetInt(name)
	return &v
}
