// Copyright 2009 by Maurice Gilden. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
 gobuild - build tool to automate building go programs
*/
package main

import (
	os "os";
	"exec";
	"flag";
	"path";
	"strings";
	"container/vector";
	"go/ast";
	"go/parser";
	"./logger";
)

// ========== command line parameters ==========

var flagLibrary *bool = flag.Bool("lib", false, "build all packages as librarys");
var flagBuildAll *bool = flag.Bool("a", false, "build all executables");
var flagTesting *bool = flag.Bool("t", false, "(not yet implemented) Build all tests");
var flagSingleMainFile *bool = flag.Bool("single-main", false, "one main file per executable");
var flagIncludeInvisible *bool = flag.Bool("include-hidden", false, "Include hidden directories");
var flagOutputFileName *string = flag.String("o", "", "output file");
var flagQuietMode *bool = flag.Bool("q", false, "only print warnings/errors");
var flagQuieterMode *bool = flag.Bool("qq", false, "only print errors");
var flagVerboseMode *bool = flag.Bool("v", false, "print debug messages");
var flagIncludePaths *string = flag.String("I", "", "additional include paths");
var flagClean *bool = flag.Bool("clean", false, "delete all temporary files");

// ========== constants ==========


// ========== structs ==========

type goPackage struct {
	name string;             // name of the package
	files *vector.Vector;    // a list of files for this package
	depends *vector.Vector;  // a list of other local packages this one depends on
	compiled bool;           // true = finished compiling
	inProgress bool;         // true = currently trying to compile dependencies (needed to find recursive dependencies)
	hasErrors bool;          // true = compiler returned an error
	outputFile string;       // filename (and maybe path) of the output files without extensions
}

type goFile struct {
	filename string;         // file name with full path
	pack *goPackage;         // the package this file belongs to
	hasMain bool;            // main function found (only true for main package)
	isCGOFile bool;          // imports "C"
}

// ========== global (package) variables ==========

var goPackageMap map[string] *goPackage;
var goMainMap map[string] *goPackage;    // map with only the main function files (one per entry)
var compilerBin string;
var linkerBin string;
var gopackBin string = "gopack";
var compileError bool = false;
var linkError bool = false;
var rootPath string;
var rootPathPerm int;
var objExt string;
var outputDirPrefix string;
var defaultOutputFileName string;

// ========== goFile methods ==========

/*
 Parses the content of a .go file and searches for package name, imports and main function.
*/
func (this *goFile) parseFile() (err os.Error) {
	var fileast *ast.File;
	var exists bool;
	var packageName string;

	fileast, err = parser.ParseFile(this.filename, nil, 0);
	packageName = fileast.Name.String();

	if err != nil {
		logger.Warn("Parsing file %s returned with errors: %s\n", this.filename, err);
	}

	// ignore main package files if building a library
	if *flagLibrary && packageName == "main" {
		return
	}

	// check if package is in the correct path
	if packageName != "main" {
		switch strings.Count(this.filename, "/") {
		case 0: // no sub-directory
			logger.Warn("File %s from package %s is not in the correct path. Should be %s.\n",
				this.filename, fileast.Name, 
				strings.Join([]string{packageName, this.filename}, "/"));
		case 1: // one sub-directory
			if this.filename[0:strings.Index(this.filename, "/")] != packageName {
				logger.Warn("File %s from package %s is not in the correct directory. Should be %s.\n",
					this.filename, packageName,
					strings.Join([]string{packageName,
					this.filename[strings.Index(this.filename, "/")+1:len(this.filename)]}, "/"));
			}
		default: // >1 sub-directories
			if this.filename[max(strings.LastIndex(this.filename, "/") - len(packageName), 0):strings.LastIndex(this.filename, "/")] != packageName {

				// NOTE: this case will result in a link-error (exit with error here?)
				logger.Warn("File %s from package %s is not in the expected directory.\n",
					this.filename, packageName);
			}
			packageName = strings.Join([]string{
				this.filename[0:strings.LastIndex(this.filename[0:strings.LastIndex(this.filename, "/")], "/")],
				packageName}, "/");
		}
	}


	if packageName != "main" {
		// check if package is already known
		this.pack, exists = goPackageMap[packageName];
		if !exists {
			// create new goPackage if it's unknown
			this.pack = newGoPackage(packageName);
			goPackageMap[this.pack.name] = this.pack;
		}
	} else {
		// main package will be stored in a new temporary package
		this.pack = newGoPackage(packageName);		
	}

	// find the local imports in this file
	// main package files will also be scanned for 
	visitor := astVisitor{this};
	ast.Walk(visitor, fileast);

	this.pack.files.Push(this);
	
	if this.hasMain && packageName == "main" {
		// main package + main function -> don't add to package list but to
		// goMainMap instead
		goMainMap[this.filename] = this.pack;

		// outputFile needs to be changed to the executable name
		if defaultOutputFileName != "" {
			this.pack.outputFile = defaultOutputFileName;
		} else {
			this.pack.outputFile = this.filename[0:len(this.filename)-3];
		}
	} else if packageName == "main" {
		// main package + no main function -> combine with existing main
		// package or create a new one
		mainPack, exists := goPackageMap[packageName];
		if !exists {
			mainPack = newGoPackage("main");
			goPackageMap["main"] = mainPack;
		}
		mainPack.append(this.pack);
		this.pack = mainPack;
	}
	
	return;
}

// ========== goPackage methods ==========

/*
 Creates a new goPackage and adds it to goPackageMap.
 Doesn't check if there's already a package with that name.
*/
func newGoPackage(name string) *goPackage {
	pack := new(goPackage);
	pack.compiled = false;
	pack.inProgress = false;
	pack.hasErrors = false;
	pack.name = name;
	pack.files = new(vector.Vector);
	pack.depends = new(vector.Vector);
	pack.outputFile = name;
	
	return pack;
}

/*
 Creates a clone of a package. Entries in files and depends are the same but with new vectors.
*/
func (this *goPackage) clone() *goPackage {
	pack := new(goPackage);
	pack.compiled = this.compiled;
	pack.inProgress = this.inProgress;
	pack.hasErrors = this.hasErrors;
	pack.name = this.name;
	pack.files = new(vector.Vector);
	this.files.Do(func(gf interface{}) {
		pack.files.Push(gf.(*goFile));
	});
	pack.depends = new(vector.Vector);
	this.depends.Do(func(dep interface{}) {
		pack.depends.Push(dep.(*goPackage));
	});
	pack.outputFile = this.outputFile;

	return pack;
}

/*
 Append a different package to this package. Will add dependencies and files, but
 keep all other things as they are.
*/
func (this *goPackage) append(pack *goPackage) {
	pack.files.Do(func(gf interface{}) {
		this.files.Push(gf.(*goFile));
	});
	pack.depends.Do(func(dep interface{}) {
		this.depends.Push(dep.(*goPackage));
	});
}

// ========== goFileVisitor ==========

// this visitor looks for files with the extension .go
type goFileVisitor struct {}

	
// implementation of the Visitor interface for the file walker
func (v *goFileVisitor) VisitDir(path string, d *os.Dir) bool {
	if path[strings.LastIndex(path, "/") + 1] == '.' {
		return *flagIncludeInvisible;
	}
	return true;
}

func (v *goFileVisitor) VisitFile(path string, d *os.Dir) {
	// parse hidden directories?
	if (path[strings.LastIndex(path, "/") + 1] == '.') && (!*flagIncludeInvisible) {
		return;
	}

	if strings.HasSuffix(path, ".go") {
		// include _test.go files?
		if strings.HasSuffix(path, "_test.go") && (!*flagTesting) {
			return;
		}

		gf := goFile{path[len(rootPath)+1:len(path)], nil, false, false};
		gf.parseFile();
	}
}

// ========== astVisitor ==========

// this visitor looks for imports and the main function in an AST
type astVisitor struct {
	file *goFile;
}

/*
 Implementation of the visitor interface for ast walker.
 Returning nil stops the walker, anything else continues into the subtree
*/
func (v astVisitor) Visit(node interface{}) (w ast.Visitor) {
	switch n := node.(type) {
	case *ast.ImportSpec:
		for _, bl := range n.Path {
			if (len(bl.Value) > 4) && 
				(bl.Value[1] == '.') &&
				(bl.Value[2] == '/'){
				
				// local package found
				packName := string(bl.Value[3:len(bl.Value)-1]);
				dep, exists := goPackageMap[packName]; 
				if !exists {
					dep = newGoPackage(packName);
					goPackageMap[dep.name] = dep;
				}
				v.file.pack.depends.Push(dep);
			}
			
			if string(bl.Value) == "\"C\"" {
				v.file.isCGOFile = true;
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
		if n.Recv == nil && n.Name.Value == "main" && v.file.pack.name == "main" {
			v.file.hasMain = true;
		}
		return nil;
	default:
		return nil;
	}

	return nil; // unreachable
}

// ========== (local) functions ==========

/*
 readFiles reads all files with the .go extension and creates their AST.
 It also creates a list of local imports (everything starting with ./)
 and searches the main package files for the main function.
*/
func readFiles(rootpath string) {
	// path walker error channel
	errorChannel := make(chan os.Error, 64);

	// visitor for the path walker
	visitor := &goFileVisitor{};
	
	logger.Info("Parsing go file(s)...\n");
	
	path.Walk(rootpath, visitor, errorChannel);
	
	if err, ok := <-errorChannel; ok {
		logger.Error("Error while traversing directories: %s\n", err);
	}
}

/*
 The compile method will run the compiler for every package it has found,
 starting with the main package.
*/
func compile(pack *goPackage) {
	var argv []string;
	var argvFilled int;

	// check for recursive dependencies
	if pack.inProgress {
		logger.Error("Found a recurisve dependency in %s. This is not supported in Go, aborting compilation.\n", pack.name);
		os.Exit(1);
	}
	pack.inProgress = true;

	// first compile all dependencies
	pack.depends.Do(func(e interface{}) {
		dep := e.(*goPackage);
		if !dep.compiled {
			compile(dep);
		}
	});

	// check if this package has any files (if not -> error)
	if pack.files.Len() == 0 {
		logger.Error("No files found for package %s.\n", pack.name);
		os.Exit(1);
	}
	
	// construct compiler command line arguments
	if (pack.name != "main") {
		logger.Info("Compiling %s...\n", pack.name);
	} else {
		logger.Info("Compiling %s (%s)...\n", pack.name, pack.outputFile);
	}
	if *flagIncludePaths != "" {
		argv = make([]string, pack.files.Len() + 5);
	} else {
		argv = make([]string, pack.files.Len() + 3);
	}

	argv[argvFilled] = compilerBin; argvFilled++;
	argv[argvFilled] = "-o"; argvFilled++;
	argv[argvFilled] = outputDirPrefix + pack.outputFile + objExt; argvFilled++;

	if *flagIncludePaths != "" {
		argv[argvFilled] = "-I"; argvFilled++;
		argv[argvFilled] = *flagIncludePaths; argvFilled++;
	}

	logger.Info("\tfiles: ");
	for i := 0; i < pack.files.Len(); i++  {
		gf := pack.files.At(i).(*goFile);
		argv[argvFilled] = gf.filename;
		logger.Info("%s ", argv[argvFilled]);
		argvFilled++;
	}
	logger.Info("\n");
		
	cmd, err := exec.Run(compilerBin, argv[0:argvFilled], os.Environ(), exec.DevNull, 
		exec.PassThrough, exec.PassThrough);
	if err != nil {
		logger.Error("%s\n", err);
		os.Exit(1);
	}

	waitmsg, err := cmd.Wait(0);
	if err != nil {
		logger.Error("Compiler execution error (%s), aborting compilation.\n", err);
		os.Exit(1);
	}

	if waitmsg.ExitStatus() != 0 {
		compileError = true;
		pack.hasErrors = true;
	}
	
	// it should now be compiled
	pack.compiled = true;
	pack.inProgress = false;

}

/*
 Calls the linker for the main file, which should be called "main.(5|6|8)".
*/
func link(pack *goPackage) {
	var argv []string;

	if *flagIncludePaths != "" {
		argv = make([]string, 6);
		argv = []string{
			linkerBin,
			"-o",
			outputDirPrefix + pack.outputFile,
			"-L",
			*flagIncludePaths,
			outputDirPrefix + pack.outputFile + objExt};
		
	} else {
		argv = make([]string, 4);
		argv = []string{
			linkerBin,
			"-o",
			outputDirPrefix + pack.outputFile,
			outputDirPrefix + pack.outputFile + objExt};

	}
	
	logger.Info("Linking %s...\n", argv[2]);

	cmd, err := exec.Run(linkerBin, argv, os.Environ(),
		exec.DevNull, exec.PassThrough, exec.PassThrough);
	if err != nil {
		logger.Error("%s\n", err);
		os.Exit(1);
	}
	waitmsg, err := cmd.Wait(0);
	if err != nil {
		logger.Error("Linker execution error (%s), aborting compilation.\n", err);
		os.Exit(1);
	}

	if waitmsg.ExitStatus() != 0 {
		logger.Error("Linker returned with errors, aborting.\n");
		os.Exit(1);
	}
}

func packLib(pack *goPackage) {

	logger.Info("Creating %s.a...\n", pack.name);

	argv := []string{
		gopackBin,
		"crg", // create new go archive
		outputDirPrefix + pack.name + ".a",
		outputDirPrefix + pack.name + objExt};

	cmd, err := exec.Run(gopackBin, argv, os.Environ(),
		exec.DevNull, exec.PassThrough, exec.PassThrough);
	if err != nil {
		logger.Error("%s\n", err);
		os.Exit(1);
	}
	waitmsg, err := cmd.Wait(0);
	if err != nil {
		logger.Error("gopack execution error (%s), aborting.\n", err);
		os.Exit(1);
	}

	if waitmsg.ExitStatus() != 0 {
		logger.Error("gopack returned with errors, aborting.\n");
		os.Exit(1);
	}

}

/*
 Build an executable from the given sources.
*/
func buildExecutable() {
	// check if there's a main package:
	if len(goMainMap) == 0 {
		logger.Error("No main package found.\n");
		os.Exit(1);
	}

	// multiple main, no command file from command line and no -a -> error
	if (len(goMainMap) > 1) && (flag.NArg() == 0) && !*flagBuildAll {
		logger.Error("Multiple files found with main function.\n");
		logger.ErrorContinue("Please specify one or more as command line parameter or\n");
		logger.ErrorContinue("run gobuild with -a. Available main files are:\n");
		for fn, _ := range goMainMap {
			logger.ErrorContinue("\t %s\n", fn);
		}
		os.Exit(1);
	}
	
	// compile all needed packages
	if flag.NArg() > 0 {
		for _, fn := range flag.Args() {
			mainPack, exists := goMainMap[fn];
			if !exists {
				logger.Error("File %s not found.\n", fn);
				return; // or os.Exit?
			}
			
			if !*flagSingleMainFile {
				pack, exists := goPackageMap["main"];
				if exists {
					mainPack.append(pack);
				}
			}
			compile(mainPack);

			// link everything together
			if !compileError {
				link(mainPack);
			} else {
				logger.Error("Can't link executable because of compile errors.\n");
			}
		}
	} else {
		for _, mainPack := range goMainMap {

			if !*flagSingleMainFile {
				pack, exists := goPackageMap["main"];
				if exists {
					mainPack.append(pack);
				}
			}
			compile(mainPack);

			// link everything together
			if !compileError {
				link(mainPack);
			} else {
				logger.Error("Can't link executable because of compile errors.\n");
			}
		}
	}
	

}


/*
 Build library files (.a) for all packages or the ones given though
 command line parameters.
*/
func buildLibrary() {
	var packNames []string;
	var pack *goPackage;
	var exists bool;

	if len(goPackageMap) == 0 {
		logger.Warn("No packages found to build.\n");
		return;
	}

	// check for command line parameters
	if flag.NArg() > 0 {
		packNames = flag.Args();
	} else {
		var i int;
		packNames = make([]string, len(goPackageMap));
		for name, _ := range goPackageMap {
			packNames[i] = name;
			i++;
		}
	}


	// loop over all packages, compile them and build a .a file
	for _, name := range packNames {

		if name == "main" {
			continue; // don't make this into a library
		}
		
		pack, exists = goPackageMap[name];
		if !exists {
			logger.Error("Package %s doesn't exist.\n", name);
			continue; // or exit?
		}
		
		// these packages come from invalid/unhandled imports
		if pack.files.Len() == 0 {
			logger.Debug("Skipping package %s, no files to compile.\n", pack.name);
			continue;
		}

		if !pack.compiled {
			logger.Debug("Building %s...\n", pack.name);
			compile(pack);
			packLib(pack);
		}
	}

}

/*
 This function does exactly the same as "make clean".
*/
func clean() {
	bashBin, err := exec.LookPath("bash");
	if err != nil {
		logger.Error("Need bash to clean.\n");
		os.Exit(1);
	}

	argv := []string{bashBin, "-c", "commandhere"};

	if *flagVerboseMode {
		argv[2] = "rm -rfv *.[568vqo] *.a [568vq].out *.cgo1.go *.cgo2.c _cgo_defun.c _cgo_gotypes.go *.so _obj _test _testmain.go";
	} else {
		argv[2] = "rm -rf *.[568vqo] *.a [568vq].out *.cgo1.go *.cgo2.c _cgo_defun.c _cgo_gotypes.go *.so _obj _test _testmain.go";
	}
	
	logger.Info("Running: %v\n", argv[2:]);

	cmd, err := exec.Run(bashBin, argv, os.Environ(),
		exec.DevNull, exec.PassThrough, exec.PassThrough);
	if err != nil {
		logger.Error("%s\n", err);
		os.Exit(1);
	}
	waitmsg, err := cmd.Wait(0);
	if err != nil {
		logger.Error("Couldn't delete files: %s\n", err);
		os.Exit(1);
	}

	if waitmsg.ExitStatus() != 0 {
		logger.Error("rm returned with errors.\n");
		os.Exit(1);
	}


}


// Returns the bigger number.
func max(a, b int) int {
	if a > b {
		return a;
	}
	return b;
}

func main() {
	var err os.Error;
	var rootPathDir *os.Dir;

	// parse command line arguments
	flag.Parse();

	if *flagQuieterMode {
		logger.SetVerbosityLevel(logger.ERROR);
	} else if *flagQuietMode {
		logger.SetVerbosityLevel(logger.WARN);
	} else if *flagVerboseMode {
		logger.SetVerbosityLevel(logger.DEBUG);
	}

	if *flagClean {
		clean();
		os.Exit(0);
	}
	
	// get the compiler/linker executable
	switch os.Getenv("GOARCH") {
	case "amd64":
		compilerBin = "6g";
		linkerBin = "6l";
		objExt = ".6";
	case "386":
		compilerBin = "8g";
		linkerBin = "8l";
		objExt = ".8";
	case "arm":
		compilerBin = "5g";
		linkerBin = "5l";
		objExt = ".5";
	default:
		logger.Error("Please specify a valid GOARCH (amd64/386/arm).\n");
		os.Exit(1);		
	}

	// get the complete path to the compiler/linker
	compilerBin, err = exec.LookPath(compilerBin);
	if err != nil {
		logger.Error("Could not find compiler %s: %s\n", compilerBin, err);
		os.Exit(1);
	}
	linkerBin, err = exec.LookPath(linkerBin);
	if err != nil {
		logger.Error("Could not find linker %s: %s\n", linkerBin, err);
		os.Exit(1);
	}
	gopackBin, err = exec.LookPath(gopackBin);
	if err != nil {
		logger.Error("Could not find gopack executable (%s): %s\n", gopackBin, err);
		os.Exit(1);
	}
	
	// get the root path from where the application was called
	// and its permissions (used for subdirectories)
	if rootPath, err = os.Getwd(); err != nil {
		logger.Error("Could not get the root path: %s\n", err);
		os.Exit(1);
	}
	if rootPathDir, err = os.Stat(rootPath); err != nil {
		logger.Error("Could not read the root path: %s\n", err);
		os.Exit(1);
	}
	rootPathPerm = rootPathDir.Permission();

	// create the package & main map
	goPackageMap = make(map[string] *goPackage);
	goMainMap = make(map[string] *goPackage);

	// check if -o with path
	if *flagOutputFileName != "" {
		dir, err := os.Stat(*flagOutputFileName);
		if err != nil {
			// doesn't exist? try to make it if it's a path
			if (*flagOutputFileName)[len(*flagOutputFileName)-1] == '/' {
				err = os.MkdirAll(*flagOutputFileName, rootPathPerm);
				if err == nil {
					outputDirPrefix = *flagOutputFileName;
				}
			} else {
				defaultOutputFileName = *flagOutputFileName;
				// TODO: make sure the path to this file is created!
			}
		} else if dir.IsDirectory() {
			if (*flagOutputFileName)[len(*flagOutputFileName)-1] == '/' {
				outputDirPrefix = *flagOutputFileName;
			} else {
				outputDirPrefix = *flagOutputFileName + "/";
			}
		} else {
			defaultOutputFileName = *flagOutputFileName;
		}

		// make path to output file
		if outputDirPrefix == "" && strings.Index(*flagOutputFileName, "/") != -1 {
			err = os.MkdirAll((*flagOutputFileName)[0:strings.LastIndex(*flagOutputFileName, "/")], rootPathPerm);
			if err != nil {
				logger.Error("Could not create %s: %s\n",
					(*flagOutputFileName)[0:strings.LastIndex(*flagOutputFileName, "/")],
					err);
			}
		}

	}

	// read all go files in the current path + subdirectories and parse them
	readFiles(rootPath);

	if *flagLibrary {
		buildLibrary();
	} else {
		buildExecutable();
	}
}
