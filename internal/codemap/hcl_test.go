package codemap

import "testing"

const hclSource = `# The region this stack runs in.
variable "region" {
  default = "us-east-1"
}

resource "aws_instance" "web" {
  ami = "ami-123"

  # Keep the instance disposable.
  lifecycle {
    create_before_destroy = true
  }
}

module "vpc" {
  source = "./modules/vpc"
}

terraform {
  required_version = ">= 1.0"
}

output "id" {
  value = aws_instance.web.id
}
`

func TestOutlineHCL(t *testing.T) {
	fm, err := Outline(write(t, "main.tf", hclSource))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Lang != "hcl" || fm.Fallback || fm.ParseError {
		t.Fatalf("lang=%q fallback=%v parseError=%v, want hcl, parsed cleanly", fm.Lang, fm.Fallback, fm.ParseError)
	}

	cases := []struct {
		name       string
		start, end int
		depth      int
		sig, doc   string
	}{
		// A block is named the way the tool addresses it: the labels joined by
		// dots, and the type word alone when it takes no label.
		{"region", 1, 4, 0, `variable "region"`, "The region this stack runs in."},
		{"aws_instance.web", 6, 13, 0, `resource "aws_instance" "web"`, ""},
		// A nested block is captured too and indented under its parent.
		{"lifecycle", 9, 12, 1, "lifecycle", "Keep the instance disposable."},
		{"vpc", 15, 17, 0, `module "vpc"`, ""},
		{"terraform", 19, 21, 0, "terraform", ""},
		{"id", 23, 25, 0, `output "id"`, ""},
	}
	for _, c := range cases {
		s := find(t, fm, c.name)
		if s.StartLine != c.start || s.EndLine != c.end || s.Depth != c.depth || s.Signature != c.sig || s.Doc != c.doc {
			t.Errorf("%s = %d-%d depth %d %q doc %q, want %d-%d depth %d %q doc %q",
				c.name, s.StartLine, s.EndLine, s.Depth, s.Signature, s.Doc, c.start, c.end, c.depth, c.sig, c.doc)
		}
	}

	// Arguments are not declarations; the block's range covers them.
	for _, s := range fm.Symbols {
		switch s.Name {
		case "default", "ami", "source", "required_version", "value":
			t.Errorf("attribute %q is listed — it belongs to the block that holds it", s.Name)
		}
	}
}

// HCL is for the outline only: check_syntax must leave it unchecked, because
// the file is validated by Terraform, not by a parser.
func TestHCLIsNotSyntaxChecked(t *testing.T) {
	for _, name := range []string{"x.tf", "x.tfvars", "x.hcl"} {
		if lookup(name) != nil {
			t.Errorf("%s resolves to a checked grammar", name)
		}
		if lookupForOutline(name) == nil {
			t.Errorf("%s has no outline grammar", name)
		}
	}
}
