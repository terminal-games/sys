package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go <directory>")
		os.Exit(1)
	}

	dir := os.Args[1]
	if err := processDirectory(dir); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func processDirectory(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && strings.HasSuffix(path, ".go") {
			if err := processFile(path); err != nil {
				return fmt.Errorf("processing %s: %w", path, err)
			}
			fmt.Printf("Processed: %s\n", path)
		}

		return nil
	})
}

func processFile(filename string) error {
	fset := token.NewFileSet()
	content, err := os.ReadFile(filename)
	if err != nil {
		return err
	}

	node, err := parser.ParseFile(fset, filename, content, parser.ParseComments)
	if err != nil {
		return err
	}

	syscallFuncs := map[string]bool{
		"Syscall":           true,
		"Syscall6":          true,
		"RawSyscall":        true,
		"RawSyscall6":       true,
		"SyscallNoError":    true,
		"RawSyscallNoError": true,
	}

	type stmtInfo struct {
		pos      token.Pos
		call     *ast.CallExpr
		funcName string
	}
	var stmts []stmtInfo

	ast.Inspect(node, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.ExprStmt:
			// Handle direct calls like: SyscallNoError(...)
			if call, ok := stmt.X.(*ast.CallExpr); ok {
				if ident, ok := call.Fun.(*ast.Ident); ok {
					if syscallFuncs[ident.Name] {
						stmts = append(stmts, stmtInfo{
							pos:      stmt.Pos(),
							call:     call,
							funcName: ident.Name,
						})
					}
				}
			}
		case *ast.AssignStmt:
			// Handle assignments like: _, _, e1 := Syscall6(...)
			for _, expr := range stmt.Rhs {
				if call, ok := expr.(*ast.CallExpr); ok {
					if ident, ok := call.Fun.(*ast.Ident); ok {
						if syscallFuncs[ident.Name] {
							stmts = append(stmts, stmtInfo{
								pos:      stmt.Pos(),
								call:     call,
								funcName: ident.Name,
							})
						}
					}
				}
			}
		}
		return true
	})

	if len(stmts) == 0 {
		return nil
	}

	lines := bytes.Split(content, []byte("\n"))
	offset := 0

	for _, stmt := range stmts {
		pos := fset.Position(stmt.pos)
		lineIdx := pos.Line - 1 + offset

		if lineIdx < 0 || lineIdx >= len(lines) {
			continue
		}

		if lineIdx > 0 && bytes.Contains(lines[lineIdx-1], []byte("panic(\"syscall not supported in wasm:")) {
			continue
		}

		line := lines[lineIdx]
		indent := getIndentBytes(line)

		callText := extractCallFromAST(stmt.call, fset, content)

		panicLine := append(indent, []byte(fmt.Sprintf("panic(\"syscall not supported in wasm: %s\")", callText))...)

		lines = insertLineBytes(lines, lineIdx, panicLine)
		offset++
	}

	modified := bytes.Join(lines, []byte("\n"))
	formatted, err := format.Source(modified)
	if err != nil {
		fmt.Printf("Warning: could not format %s: %v\n", filename, err)
		return os.WriteFile(filename, modified, 0644)
	}

	return os.WriteFile(filename, formatted, 0644)
}

func extractCallFromAST(call *ast.CallExpr, fset *token.FileSet, content []byte) string {
	start := fset.Position(call.Pos()).Offset
	end := fset.Position(call.End()).Offset

	if start >= 0 && end <= len(content) && start < end {
		return string(content[start:end])
	}

	// Fallback (shouldn't happen)
	return "syscall"
}

func getIndentBytes(line []byte) []byte {
	for i := 0; i < len(line); i++ {
		if line[i] != ' ' && line[i] != '\t' {
			return line[:i]
		}
	}
	return []byte{}
}

func insertLineBytes(lines [][]byte, index int, newLine []byte) [][]byte {
	result := make([][]byte, 0, len(lines)+1)
	result = append(result, lines[:index]...)
	result = append(result, newLine)
	result = append(result, lines[index:]...)
	return result
}
