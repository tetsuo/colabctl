// Package notebook parses and represents Jupyter/Colab .ipynb files.
package notebook

import (
	"encoding/json"
	"fmt"
)

// Cell types used in notebooks.
const (
	CellTypeCode     = "code"
	CellTypeMarkdown = "markdown"
	CellTypeRaw      = "raw"
)

// Source is a notebook cell's source, which can be stored as a single string
// or a list of strings (lines) in the JSON format.
type Source []string

func (s *Source) UnmarshalJSON(data []byte) error {
	// Try string first.
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*s = Source{str}
		return nil
	}
	// Fall back to slice.
	var lines []string
	if err := json.Unmarshal(data, &lines); err != nil {
		return err
	}
	*s = Source(lines)
	return nil
}

// String joins all source lines into one string.
func (s Source) String() string {
	out := ""
	for _, l := range s {
		out += l
	}
	return out
}

// Output represents one output entry produced by a cell execution.
type Output struct {
	OutputType string          `json:"output_type"`
	Text       Source          `json:"text"`
	Data       json.RawMessage `json:"data"`
	EValue     Source          `json:"evalue"`
	Traceback  []string        `json:"traceback"`
}

// Cell is a single cell in the notebook.
type Cell struct {
	CellType string   `json:"cell_type"`
	ID       string   `json:"id"`
	Source   Source   `json:"source"`
	Outputs  []Output `json:"outputs"`
	Metadata struct {
		ExecutionCount *int `json:"execution_count"`
	} `json:"metadata"`
}

// Notebook is the top-level structure of a .ipynb file.
type Notebook struct {
	NBFormat      int `json:"nbformat"`
	NBFormatMinor int `json:"nbformat_minor"`
	Metadata      struct {
		KernelInfo struct {
			Name string `json:"name"`
		} `json:"kernelspec"`
		LanguageInfo struct {
			Name string `json:"name"`
		} `json:"language_info"`
		CoLab struct {
			Name string `json:"name"`
		} `json:"colab"`
	} `json:"metadata"`
	Cells []*Cell `json:"cells"`
}

// Parse decodes a .ipynb JSON payload into a Notebook.
func Parse(data []byte) (*Notebook, error) {
	var nb Notebook
	if err := json.Unmarshal(data, &nb); err != nil {
		return nil, fmt.Errorf("parse notebook: %w", err)
	}
	return &nb, nil
}

// CodeCells returns only the code cells in order.
func (nb *Notebook) CodeCells() []*Cell {
	var out []*Cell
	for _, c := range nb.Cells {
		if c.CellType == CellTypeCode {
			out = append(out, c)
		}
	}
	return out
}

// Title returns the notebook name from metadata if available.
func (nb *Notebook) Title() string {
	return nb.Metadata.CoLab.Name
}
