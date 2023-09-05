#!/bin/bash

# Define the arrays of OS and architecture
os=("linux" "darwin" "windows")
arch=("amd64" "arm64")

# Define the ignore list
ignore=("windows/arm64")

# apps is a map of app name to the source directory
declare -A apps
apps["zoomrs"]="../cmd/service"
apps["zoomrs-cli"]="../cmd/cli"

# version is passed in as an argument
version=$1

# Loop through the OS and architecture arrays
for o in "${os[@]}"; do
  for a in "${arch[@]}"; do
    # Check if the current combination is in the ignore list
    combination="$o/$a"
    if [[ " ${ignore[*]} " == *" $combination "* ]]; then
      echo "Ignoring $combination"
    else
	  # Build the application for the current combination
	  for app in "${!apps[@]}"; do
		source="${apps[$app]}"
		out_dir="release/zoomrs_${o}_${a}_${version}"
		mkdir -p $out_dir
		echo "Building $app for $combination into $out_dir"
		if [ "$o" == "windows" ]; then
		  app="$app.exe"
		fi
		GOOS="$o" GOARCH="$a" go build -o "$out_dir/$app" "$source"
	  done
	  cp ./README.md "$out_dir/"
	  cp ../LICENSE "$out_dir/"
	  cp ./Makefile "$out_dir/"
	  cp ./config.yml "$out_dir/"
	  cp ./zoomrs.service "$out_dir/"
	  tar -czvf "$out_dir.tar.gz" -C "$out_dir" .
	  rm -rf "$out_dir"
    fi
  done
done
