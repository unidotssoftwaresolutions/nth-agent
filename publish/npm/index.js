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
	"linux-arm64": "nth-agent-linux-arm64",
	"win32-x64": "nth-agent-windows-amd64.exe",
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

// The signals are listened for before the binary starts. Listening after it would leave a moment when the binary runs
// and a SIGINT or SIGTERM sent to this process ends it alone, leaving the binary running. A signal that comes before the
// binary has started is handled once it has, so the binary still hears it.
//
// ON WINDOWS NOTHING IS PASSED ON. A Ctrl-C in a console reaches every program attached to it, the binary included, which
// then hands its phones back and ends. Passing it on would be worse than nothing: Node can only end a Windows program
// outright, so the binary would stop before it said goodbye. Listening still keeps this process alive until the binary
// has ended, and its exit code is passed on as everywhere else.
let child;
for (const signal of ["SIGINT", "SIGTERM"]) {
	process.on(signal, () => {
		if (process.platform === "win32") {
			return;
		}
		if (child !== undefined && child.exitCode === null && child.signalCode === null) {
			child.kill(signal);
		}
	});
}

child = spawn(path.join(__dirname, "bin", binary), process.argv.slice(2), { stdio: "inherit" });

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
