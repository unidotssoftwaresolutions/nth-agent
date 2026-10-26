#!/usr/bin/env node
// Starts the nth-agent binary built for this machine with these arguments and this terminal, and ends the way it ends.
"use strict";

const { spawn } = require("node:child_process");
const { constants } = require("node:os");
const path = require("node:path");

const binaries = {
	"darwin-arm64": "nth-agent-darwin-arm64",
	"darwin-x64": "nth-agent-darwin-amd64",
	"linux-x64": "nth-agent-linux-amd64",
};

const platform = `${process.platform}-${process.arch}`;
const binary = binaries[platform];

if (binary === undefined) {
	const supported = Object.keys(binaries);
	console.error(
		`There is no nth-agent binary for ${platform}; it runs on ${supported.slice(0, -1).join(", ")} and ${supported[supported.length - 1]}.`,
	);
	process.exit(1);
}

const child = spawn(path.join(__dirname, "bin", binary), process.argv.slice(2), { stdio: "inherit" });

for (const signal of ["SIGINT", "SIGTERM"]) {
	process.on(signal, () => {
		if (child.exitCode === null && child.signalCode === null) {
			child.kill(signal);
		}
	});
}

child.on("error", (error) => {
	console.error(`${binary} could not be started: ${error.message}`);
	process.exit(1);
});

child.on("exit", (code, signal) => {
	if (signal === null) {
		process.exit(code === null ? 1 : code);
	}
	process.removeAllListeners(signal);
	process.kill(process.pid, signal);
	process.exit(128 + (constants.signals[signal] || 0));
});
