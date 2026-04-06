(function () {
	// Pre-populate meta panel from session storage (set by home.html on form submit)
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
	const terminal    = document.getElementById("terminal");
	const liveBadge   = document.getElementById("live-badge");
	const resultBanner = document.getElementById("result-banner");
	const errorBanner  = document.getElementById("error-banner");
	const publicUrlEl  = document.getElementById("public-url");
	const openUrlBtn   = document.getElementById("open-url");

	// ── Phase → pipeline step mapping ─────────────────────────────────────────
	// Maps orchestrator phase strings to data-step names on the <li> elements.
	const PHASE_STEP = {
		"analyzing":             "analyze",
		"generating-dockerfile": "generate",
		"security-scan":         "secure",
		"configuring":           "configure",
		"building":              "build",
		"deploying":             "deploy",
	};

	// Human-readable labels for the activity log
	const PHASE_LABEL = {
		"analyzing":             "Analyzing repository with AI…",
		"generating-dockerfile": "Generating Dockerfile with AI…",
		"security-scan":        "Running security scan…",
		"configuring":          "Optimizing K8s configuration with AI…",
		"building":             "Building image with Kaniko…",
		"deploying":            "Deploying workload to Kubernetes…",
	};

	// ── Utility: escape HTML ───────────────────────────────────────────────────
	function escapeHtml(s) {
		const d = document.createElement("div");
		d.textContent = s;
		return d.innerHTML;
	}

	// ── Utility: log a timestamped line to the terminal ───────────────────────
	function logLine(line) {
		const t  = new Date();
		const ts = t.toTimeString().slice(0, 8);
		const row = document.createElement("div");
		row.className = "terminal-line";
		row.innerHTML = `<span class="terminal-ts">${ts}</span><span class="terminal-msg">${escapeHtml(line)}</span>`;
		terminal.appendChild(row);
		terminal.scrollTop = terminal.scrollHeight;
	}

	// ── Utility: show/hide an AI step and update its badge ───────────────────
	function revealStep(step) {
		const el = document.querySelector(`[data-step="${step}"]`);
		if (!el) return;
		el.style.display = "";
	}

	function setStep(step, state, className) {
		const el = document.querySelector(`[data-step="${step}"]`);
		if (!el) return;
		// Show hidden AI steps as soon as we touch them
		el.style.display = "";
		const badge = el.querySelector(".pipeline-step__state");
		if (badge) {
			badge.textContent = state;
			badge.className = "pipeline-step__state pipeline-step__state--" + (className || "idle");
		}
		el.classList.remove("is-active", "is-done", "is-error");
		if (className === "active") el.classList.add("is-active");
		if (className === "done")   el.classList.add("is-done");
		if (className === "error")  el.classList.add("is-error");
	}

	// Marks the previously-active AI step done and sets the new one as active.
	// Returns the data-step name that was activated (or null).
	var _lastAiStep = null;
	function activatePhase(phase) {
		const step = PHASE_STEP[phase];
		if (!step) return null;

		// Finish off the previous active step
		if (_lastAiStep && _lastAiStep !== step) {
			setStep(_lastAiStep, "✓", "done");
		}
		_lastAiStep = step;
		setStep(step, "…", "active");
		return step;
	}

	// ── Meta panel fill ───────────────────────────────────────────────────────
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

	// ── Done / Error banners ─────────────────────────────────────────────────
	function showDone(url) {
		document.getElementById("pipeline-title").textContent = "Deployment complete";
		document.getElementById("pipeline-sub").textContent = "Your app is live on the cluster.";
		setStep("connect", "✓", "done");
		// Mark any previously active AI step done
		if (_lastAiStep) setStep(_lastAiStep, "✓", "done");
		setStep("build",  "✓", "done");
		setStep("deploy", "✓", "done");
		setStep("live",   "✓", "done");
		resultBanner.classList.remove("hidden");
		publicUrlEl.textContent = url;
		openUrlBtn.href         = url;
		liveBadge.textContent   = "● done";
		liveBadge.classList.add("terminal-live--done");
	}

	function showError(msg) {
		errorBanner.classList.remove("hidden");
		errorBanner.textContent = msg;
		liveBadge.textContent   = "● failed";
		liveBadge.classList.add("terminal-live--err");
		// Mark the active step as errored
		if (_lastAiStep) setStep(_lastAiStep, "✗", "error");
		else             setStep("build", "✗", "error");
	}

	// ── Monitor message handler ───────────────────────────────────────────────
	function handleMonitor(msg) {
		const step = document.querySelector('[data-step="monitor"]');
		if (!step) return;

		// Show the monitor step
		step.style.display = "";

		const code    = msg.healthStatus || 0;
		const notes   = msg.notes        || "";
		const isOk    = code >= 200 && code < 400;
		const isWarn  = code >= 400 && code < 500;

		let badgeCls  = isOk ? "health-ok" : (isWarn ? "health-warn" : "health-err");
		let stateText = isOk ? "✓" : (code === 0 ? "?" : "!");
		let stateCls  = isOk ? "done" : "error";

		setStep("monitor", stateText, stateCls);

		const notesEl = document.getElementById("monitor-notes");
		if (notesEl) {
			notesEl.style.display = "";
			notesEl.innerHTML =
				`<span class="health-badge ${badgeCls}">HTTP ${code || "N/A"}</span>` +
				escapeHtml(notes);
		}

		logLine(`[Monitor] HTTP ${code} — ${notes.slice(0, 120)}${notes.length > 120 ? "…" : ""}`);
	}

	// ── WebSocket setup ───────────────────────────────────────────────────────
	setStep("connect", "…", "active");
	logLine("Opening WebSocket to /start-processing …");

	const websocket = new WebSocket(wsProto + "//" + window.location.host + "/start-processing");

	websocket.addEventListener("open", () => {
		setStep("connect", "✓", "done");
		logLine("Connected. Waiting for orchestrator metadata…");
	});

	websocket.addEventListener("message", function (e) {
		const raw = (e && e.data) ? String(e.data) : "";
		if (!raw.trim()) return;

		let msg = null;
		try {
			msg = JSON.parse(raw);
		} catch (_) {
			handleLegacy(raw);
			return;
		}

		// ── meta ──────────────────────────────────────────────────────────────
		if (msg.type === "meta") {
			fillMeta(msg);
			logLine("Project " + msg.projectId + " — cloning " + (msg.repository || "") + " for build.");
			return;
		}

		// ── phase ─────────────────────────────────────────────────────────────
		if (msg.type === "phase") {
			const p      = String(msg.phase || "").toLowerCase();
			const label  = PHASE_LABEL[p] || ("Phase: " + (msg.phase || ""));
			logLine(label);
			activatePhase(p);

			// When the "building" phase starts, mark the AI pipeline complete
			if (p === "building" && _lastAiStep && _lastAiStep !== "build") {
				setStep(_lastAiStep, "✓", "done");
				_lastAiStep = "build";
			}
			// When "deploying" starts, mark build done
			if (p === "deploying") {
				setStep("build",  "✓", "done");
				setStep("deploy", "…", "active");
				_lastAiStep = "deploy";
			}
			return;
		}

		// ── done ──────────────────────────────────────────────────────────────
		if (msg.type === "done" && msg.url) {
			logLine("Done. Public URL: " + msg.url);
			showDone(msg.url);
			return;
		}

		// ── error ─────────────────────────────────────────────────────────────
		if (msg.type === "error") {
			logLine("ERROR: " + (msg.message || "unknown"));
			showError(msg.message || "Deployment failed");
			return;
		}

		// ── monitor ───────────────────────────────────────────────────────────
		if (msg.type === "monitor") {
			handleMonitor(msg);
			return;
		}

		logLine(JSON.stringify(msg));
	});

	// ── Legacy plain-text protocol (backward compat) ──────────────────────────
	function handleLegacy(raw) {
		const msg = raw.trim();
		logLine(msg);
		if (msg === "building") {
			setStep("build", "…", "active");
		}
		if (msg === "deploying") {
			setStep("build",  "✓", "done");
			setStep("deploy", "…", "active");
		}
		if (msg === "deployed") {
			setStep("deploy", "✓", "done");
			setStep("live",   "…", "active");
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
		const isFinished =
			!resultBanner.classList.contains("hidden") ||
			!errorBanner.classList.contains("hidden");
		if (!isFinished) {
			liveBadge.textContent = "● disconnected";
		}
	});

	websocket.addEventListener("error", () => {
		logLine("WebSocket error — is the server running?");
		setStep("connect", "✗", "error");
		showError("Could not connect to the deployment stream.");
	});
})();
