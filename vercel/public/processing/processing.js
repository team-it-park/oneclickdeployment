(function () {
	// Show cached form values immediately (server meta arrives on connect)
	try {
		const r = sessionStorage.getItem("deploy_repo");
		if (r) document.getElementById("meta-repo").textContent = r;
		const gr = sessionStorage.getItem("deploy_git_ref");
		if (gr) document.getElementById("meta-ref").textContent = gr;
		const df = sessionStorage.getItem("deploy_dockerfile");
		if (df) document.getElementById("meta-dockerfile").textContent = df;
		const cp = sessionStorage.getItem("deploy_cp");
		const sp = sessionStorage.getItem("deploy_sp");
		if (cp || sp) {
			document.getElementById("meta-ports").textContent =
				"container " + (cp || "·") + " → service " + (sp || "·");
		}
	} catch (_) {}

	const wsProto = window.location.protocol === "https:" ? "wss:" : "ws:";
	const terminal = document.getElementById("terminal");
	const liveBadge = document.getElementById("live-badge");
	const resultBanner = document.getElementById("result-banner");
	const errorBanner = document.getElementById("error-banner");
	const publicUrlEl = document.getElementById("public-url");
	const openUrlBtn = document.getElementById("open-url");

	/** @param {string} line */
	function logLine(line) {
		const t = new Date();
		const ts = t.toTimeString().slice(0, 8);
		const row = document.createElement("div");
		row.className = "terminal-line";
		row.innerHTML = `<span class="terminal-ts">${ts}</span><span class="terminal-msg">${escapeHtml(line)}</span>`;
		terminal.appendChild(row);
		terminal.scrollTop = terminal.scrollHeight;
	}

	function escapeHtml(s) {
		const d = document.createElement("div");
		d.textContent = s;
		return d.innerHTML;
	}

	function setStep(step, state, className) {
		const el = document.querySelector(`[data-step="${step}"]`);
		if (!el) return;
		const badge = el.querySelector(".pipeline-step__state");
		if (badge) {
			badge.textContent = state;
			badge.className = "pipeline-step__state pipeline-step__state--" + (className || "idle");
		}
		el.classList.remove("is-active", "is-done", "is-error");
		if (className === "active") el.classList.add("is-active");
		if (className === "done") el.classList.add("is-done");
		if (className === "error") el.classList.add("is-error");
	}

	function fillMeta(data) {
		const repo = data.repository || sessionStorage.getItem("deploy_repo") || "—";
		document.getElementById("meta-repo").textContent = repo;
		document.getElementById("meta-project").textContent = data.projectId || "—";
		document.getElementById("meta-ref").textContent = data.gitRef || "(orchestrator default)";
		document.getElementById("meta-dockerfile").textContent =
			data.dockerfile || (data.inlineDockerfile ? "(inline)" : "Dockerfile (default)");
		let ports = "—";
		if (data.containerPort || data.servicePort) {
			ports = `container ${data.containerPort || "·"} → service ${data.servicePort || "·"}`;
		}
		document.getElementById("meta-ports").textContent = ports;
	}

	function showDone(url) {
		document.getElementById("pipeline-title").textContent = "Deployment complete";
		document.getElementById("pipeline-sub").textContent = "Your app is live on the cluster.";
		setStep("connect", "✓", "done");
		setStep("build", "✓", "done");
		setStep("deploy", "✓", "done");
		setStep("live", "✓", "done");
		resultBanner.classList.remove("hidden");
		publicUrlEl.textContent = url;
		openUrlBtn.href = url;
		liveBadge.textContent = "● done";
		liveBadge.classList.add("terminal-live--done");
	}

	function showError(msg) {
		errorBanner.classList.remove("hidden");
		errorBanner.textContent = msg;
		liveBadge.textContent = "● failed";
		liveBadge.classList.add("terminal-live--err");
	}

	// Initial: connect step
	setStep("connect", "…", "active");
	logLine("Opening WebSocket to /start-processing …");

	const websocket = new WebSocket(wsProto + "//" + window.location.host + "/start-processing");

	websocket.addEventListener("open", () => {
		setStep("connect", "✓", "done");
		setStep("build", "…", "active");
		logLine("Connected. Waiting for orchestrator metadata…");
	});

	websocket.addEventListener("message", function (e) {
		const raw = (e && e.data) ? String(e.data) : "";
		if (!raw.trim()) return;

		let msg = null;
		try {
			msg = JSON.parse(raw);
		} catch (_) {
			// legacy plain-text protocol
			handleLegacy(raw);
			return;
		}

		if (msg.type === "meta") {
			fillMeta(msg);
			logLine("Project " + msg.projectId + " — cloning " + (msg.repository || "") + " for build.");
			return;
		}
		if (msg.type === "phase") {
			const p = String(msg.phase || "").toLowerCase();
			logLine("Phase: " + (msg.phase || ""));
			if (p === "building") {
				setStep("build", "…", "active");
				setStep("deploy", "…", "idle");
			}
			if (p === "deploying") {
				setStep("build", "✓", "done");
				setStep("deploy", "…", "active");
			}
			return;
		}
		if (msg.type === "done" && msg.url) {
			setStep("build", "✓", "done");
			setStep("deploy", "✓", "done");
			setStep("live", "✓", "done");
			logLine("Done. Public URL: " + msg.url);
			showDone(msg.url);
			return;
		}
		if (msg.type === "error") {
			logLine("ERROR: " + (msg.message || "unknown"));
			setStep("build", "✗", "error");
			showError(msg.message || "Deployment failed");
			return;
		}
		logLine(JSON.stringify(msg));
	});

	function handleLegacy(raw) {
		const msg = raw.trim();
		logLine(msg);
		if (msg === "building") {
			setStep("build", "…", "active");
		}
		if (msg === "deploying") {
			setStep("build", "✓", "done");
			setStep("deploy", "…", "active");
		}
		if (msg === "deployed") {
			setStep("deploy", "✓", "done");
			setStep("live", "…", "active");
		}
		if (msg.toLowerCase().startsWith("website endpoint is:")) {
			const url = msg.split(":", 2).slice(1).join(":").trim();
			showDone(url);
		}
		if (msg.toLowerCase().startsWith("error:")) {
			showError(msg.replace(/^error:\s*/i, ""));
		}
	}

	websocket.addEventListener("close", () => {
		logLine("WebSocket closed.");
		if (!resultBanner.classList.contains("hidden") || !errorBanner.classList.contains("hidden")) return;
		liveBadge.textContent = "● disconnected";
	});

	websocket.addEventListener("error", () => {
		logLine("WebSocket error — is the server running?");
		setStep("connect", "✗", "error");
		showError("Could not connect to the deployment stream.");
	});
})();
