package cli

import (
	"fmt"
	"time"
)

// DriftItem represents a drift entry for display
type DriftItem struct {
	ID               string
	Phase            string
	ParentAPIVersion string
	ParentKind       string
	ParentNamespace  string
	ParentName       string
	ChildAPIVersion  string
	ChildKind        string
	ChildNamespace   string
	ChildName        string
	User             string
	Operation        string
	DetectedAt       time.Time
}

// FilterValue returns the value to filter on
func (d DriftItem) FilterValue() string {
	return fmt.Sprintf("%s/%s %s/%s", d.ParentNamespace, d.ParentName, d.ChildNamespace, d.ChildName)
}

// Title returns the title for the list item
func (d DriftItem) Title() string {
	return fmt.Sprintf("%s/%s", d.ChildKind, d.ChildName)
}

// Description returns the description for the list item
func (d DriftItem) Description() string {
	phase := phaseDetectedStyle.Render("DRIFT")
	if d.Phase == "Resolved" {
		phase = phaseResolvedStyle.Render("RESOLVED")
	}
	return fmt.Sprintf("%s  parent: %s/%s  by: %s", phase, d.ParentKind, d.ParentName, d.User)
}
