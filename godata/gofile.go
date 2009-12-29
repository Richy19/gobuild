// Copyright 2009 by Maurice Gilden. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package godata

import (
	"strings";
	"os";
	"go/ast";
	"go/parser";
	"./logger";
)

var DefaultOutputFileName string;

// ================================
// ============ GoFile ============
// ================================

type GoFile struct {
	Filename string;         // file name with full path
	Pack *GoPackage;         // the package this file belongs to
	HasMain bool;            // main function found (only true for main package)
	IsCGOFile bool;          // imports "C"
}

// Returns the greater of both numbers.
func max(a, b int) int {
	if a > b {
		return a;
	}
	return b;
}

/*
 Parses the content of a .go file and searches for package name, imports and
 main function.
*/
func (this *GoFile) ParseFile(packs *GoPackageContainer) (err os.Error) {
	var fileast *ast.File;
	var packName string;

	fileast, err = parser.ParseFile(this.Filename, nil, 0);
	packName = fileast.Name.String();

	if err != nil {
		logger.Warn("Parsing file %s returned with errors: %s\n", this.Filename, err);
	}

	// check if package is in the correct path
	if packName != "main" {
		switch strings.Count(this.Filename, "/") {
		case 0: // no sub-directory
			logger.Warn("File %s from package %s is not in the correct path. Should be %s.\n",
				this.Filename, fileast.Name, 
				strings.Join([]string{packName, this.Filename}, "/"));
		case 1: // one sub-directory
			if this.Filename[0:strings.Index(this.Filename, "/")] != packName {
				logger.Warn("File %s from package %s is not in the correct directory. Should be %s.\n",
					this.Filename, packName,
					strings.Join([]string{packName,
					this.Filename[strings.Index(this.Filename, "/")+1:len(this.Filename)]}, "/"));
			}
		default: // >1 sub-directories
			if this.Filename[max(strings.LastIndex(this.Filename, "/") - len(packName), 0):strings.LastIndex(this.Filename, "/")] != packName {

				// NOTE: this case will result in a link-error (exit with error here?)
				logger.Warn("File %s from package %s is not in the expected directory.\n",
					this.Filename, packName);
			}
			packName = strings.Join([]string{
				this.Filename[0:strings.LastIndex(this.Filename[0:strings.LastIndex(this.Filename, "/")], "/")],
				packName}, "/");
		}
	}
	
	// create empty temporary package, will be merged later
	this.Pack = NewGoPackage(packName);

	// find the local imports in this file
	visitor := astVisitor{this, packs};
	ast.Walk(visitor, fileast);
	
	packs.AddFile(this, packName);
	
	return;
}

// ================================
// ========== astVisitor ==========
// ================================

// this visitor looks for imports and the main function in an AST
type astVisitor struct {
	file *GoFile;
	packs *GoPackageContainer;
}

/*
 Implementation of the visitor interface for ast walker.
 Returning nil stops the walker, anything else continues into the subtree
*/
func (v astVisitor) Visit(node interface{}) (w ast.Visitor) {
	switch n := node.(type) {
	case *ast.ImportSpec:
		var packName string;
		var packType int;
		for _, bl := range n.Path {
			if (len(bl.Value) > 4) && 
				(bl.Value[1] == '.') &&
				(bl.Value[2] == '/'){
				
				// local package found
				packName = string(bl.Value[3:len(bl.Value)-1]);
				packType = LOCAL_PACKAGE;
				
			} else {
				packName = string(bl.Value[1:len(bl.Value)-1]);
				packType = UNKNOWN_PACKAGE;
			}

			dep, exists := v.packs.Get(packName);
			if !exists {
				dep = v.packs.AddNewPackage(packName);
			} else if dep.Type == LOCAL_PACKAGE {
				packType = LOCAL_PACKAGE;
			}
			
			dep.Type = packType;
			v.file.Pack.Depends.Push(dep);

			if string(bl.Value) == "\"C\"" {
				v.file.IsCGOFile = true; // not used yet
			}

		}
		return nil;
	case *ast.Package:
		return v;
	case *ast.File:
		return v;
	case *ast.BadDecl:
		return v;
	case *ast.GenDecl:
		return v;
	case *ast.Ident:
		return nil;
	case *ast.FuncDecl:
		if n.Recv == nil && n.Name.Value == "main" && v.file.Pack.Name == "main" {
			v.file.HasMain = true;
		}
		return nil;
	default:
		return nil;
	}

	return nil; // unreachable
}