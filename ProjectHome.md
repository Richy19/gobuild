## Description ##

gobuild is an automatic build tool that aims to replace Makefiles for simple projects written in the Go programming language. It creates a dependency graph of all local imports and compiles them in the right order using the gc Go compiler.

## Compiling gobuild ##

gobuild comes with a Makefile, so running `make` and `make install` will build and install it.

If you have an older version of gobuild, running gobuild without parameters inside the source directory should be enough to compile it.

## Usage ##

Only pure Go code is supported right now. Building something that needs C code won't work.

### Building an executable ###

For most simple applications it should be enough to run gobuild without parameters. If there are multiple main functions in different files you have to give the file name for one or more of these files in the command line or you can run gobuild with the `-a` option.

Files that are inside the main package but don't have a main function will be included automatically. To prevent this you can use the option `-single-main`.

### Building a library ###

Library files (.a) can be build by running `gobuild -lib` inside the source directory of your files. Each package will result in its own library file. If you want to build only certain packages you can give their names as parameters to gobuild.

### Testing/Benchmarks ###

If there are any files ending in `_`test.go you can build a test-executable with `gobuild -t`. This will create a new executable called `_`testmain. Test/Benchmark functions must match the official naming convention, see http://golang.org/doc/code.html#Testing for more details.

To run the test you can either run `gobuild -t -run` or run the `_`testmain executable after running `gobuild -t`. Both methods can have any of the these additional command line options: `-match/-benchmarks/-v`.
Without any options, all tests will be run but none of the benchmarks. To
run all benchmarks, use `-benchmarks="."`.

The output of a gobuild test-executable is a bit different from what gotest does. In addition to "PASS"/"FAIL" or timing values it will also print out the package name that is being tested/benchmarked. The exit code will still be 1 if any test failed.

For more details and additional command line parameters please read the README file.