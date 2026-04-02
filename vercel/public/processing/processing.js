window.addEventListener("DOMContentLoaded", (_) => { 
	let websocket = new WebSocket("ws://" + window.location.host + "/start-processing");
	let room = document.getElementById("status");

	const addLine = (kind, title, desc) => {
		const line = document.createElement("div");
		line.className = "line " + kind;
		const dot = document.createElement("div");
		dot.className = "dot";
		const body = document.createElement("div");
		const t = document.createElement("div");
		t.className = "title";
		t.textContent = title;
		body.appendChild(t);
		if (desc) {
			const d = document.createElement("div");
			d.className = "desc";
			d.textContent = desc;
			body.appendChild(d);
		}
		line.appendChild(dot);
		line.appendChild(body);
		room.appendChild(line);
		room.scrollTop = room.scrollHeight;
	};

	addLine("warn", "Connecting…", "Waiting for backend WebSocket");

	websocket.addEventListener("open", () => {
		addLine("ok", "Connected", "Streaming deployment status");
	});

	websocket.addEventListener("message", function (e) {
		const data = (e && e.data) ? String(e.data) : "";
		const msg = data.trim();
		if (!msg) return;

		if (msg === "building") {
			addLine("warn", "Building image", "Kaniko is building and pushing to Harbor");
			return;
		}
		if (msg === "deploying") {
			addLine("warn", "Deploying to Kubernetes", "Applying Deployment, Service, and Ingress");
			return;
		}
		if (msg === "deployed") {
			addLine("ok", "Deployed", "Waiting for public URL…");
			return;
		}
		if (msg.toLowerCase().startsWith("website endpoint is:")) {
			const url = msg.split(":", 2).slice(1).join(":").trim();
			addLine("ok", "Public URL", url || msg);
			return;
		}
		if (msg.toLowerCase().startsWith("error:")) {
			addLine("err", "Deploy failed", msg.replace(/^error:\s*/i, ""));
			return;
		}

		addLine("warn", "Status", msg);
	});

	websocket.addEventListener("close", () => {
		addLine("warn", "Disconnected", "WebSocket closed");
	});
	websocket.addEventListener("error", () => {
		addLine("err", "WebSocket error", "Could not connect to /start-processing");
	});
});
