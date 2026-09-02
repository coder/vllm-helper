// The add-model page: find something this machine can actually run.
//
// Everything here is shaped around one question -- will it fit, and will it
// start -- because that is what separates a model you can use from a 100 GB
// download that OOMs. Search results cannot answer it (the Hub's listing has
// no sizes), so picking one measures it, and the answer gets the whole right
// column rather than a line of small print.
"use strict";

const el = {
  budgetLine: $("[data-budget-line]"),
  form: $("[data-search-form]"),
  input: $("[data-search-input]"),
  btn: $("[data-search-btn]"),
  error: $("[data-search-error]"),
  hits: $("[data-hits]"),
  resultsNote: $("[data-results-note]"),

  empty: $("[data-detail-empty]"),
  detail: $("[data-detail]"),
  title: $("[data-detail-title]"),
  link: $("[data-detail-link]"),
  loading: $("[data-detail-loading]"),
  body: $("[data-detail-body]"),
  verdict: $("[data-verdict]"),
  facts: $("[data-facts]"),
  flags: $("[data-flags]"),
  dropped: $("[data-dropped]"),
  detailError: $("[data-detail-error]"),
  pull: $("[data-pull]"),
  pullNote: $("[data-pull-note]"),
};

// The constraint is stated before any results, so the numbers below are read
// in context instead of being judged against nothing.
async function fillBudget() {
  try {
    const m = await api("/api/machine");
    if (!m.budget_gb) return;
    el.budgetLine.textContent = `This machine can serve models up to about ${m.budget_gb} GB of weights — ${m.budget_reason}`;
    el.budgetLine.hidden = false;
  } catch {
    // Not knowing the budget is survivable; the detail view says so too.
  }
}

// ---------- results ----------

function renderHits(results, query) {
  el.hits.innerHTML = "";
  if (!results.length) {
    el.resultsNote.textContent = `Nothing matched “${query}”. Try different terms.`;
    el.resultsNote.hidden = false;
    return;
  }
  el.resultsNote.textContent = `${results.length} results for “${query}”, most downloaded first.`;
  el.resultsNote.hidden = false;

  for (const r of results) {
    const li = document.createElement("li");
    const btn = document.createElement("button");
    btn.className = "hit";
    btn.type = "button";
    btn.dataset.repo = r.id;

    const id = document.createElement("span");
    id.className = "hit-id";
    id.textContent = r.id;

    const meta = document.createElement("span");
    meta.className = "hit-meta";
    meta.textContent = `${fmtCount(r.downloads)} downloads · ${fmtCount(r.likes)} likes · ${r.license}`;

    btn.append(id, meta);
    if (r.quant_tags?.length) {
      const q = document.createElement("span");
      q.className = "hit-quant";
      q.textContent = r.quant_tags.join(" ");
      btn.append(q);
    }
    btn.addEventListener("click", () => select(btn, r.id));
    li.append(btn);
    el.hits.append(li);
  }
}

el.form.addEventListener("submit", async (e) => {
  e.preventDefault();
  const q = el.input.value.trim();
  if (!q) return;
  el.error.hidden = true;
  el.btn.disabled = true;
  el.btn.textContent = "Searching…";
  el.hits.innerHTML = "";
  el.resultsNote.hidden = true;
  try {
    const { results } = await api(`/api/search?q=${encodeURIComponent(q)}`);
    renderHits(results, q);
  } catch (err) {
    el.error.textContent = err.message;
    el.error.hidden = false;
  } finally {
    el.btn.disabled = false;
    el.btn.textContent = "Search";
  }
});

// ---------- detail ----------

let selected = null;

function select(btn, repo) {
  document
    .querySelectorAll(".hit[aria-current]")
    .forEach((b) => b.removeAttribute("aria-current"));
  btn.setAttribute("aria-current", "true");
  inspect(repo);
}

function fact(term, value) {
  const dt = document.createElement("dt");
  dt.textContent = term;
  const dd = document.createElement("dd");
  dd.textContent = value;
  el.facts.append(dt, dd);
}

// The verdict repeats the CLI's wording on purpose. Someone who has read
// 'inspect' output in a terminal should not have to learn a second vocabulary
// for the same judgement, and the error direction matters: over-reporting a
// size is an annoyance, under-reporting it gets a huge download and an OOM.
function renderVerdict(d) {
  const of =
    d.variants > 1 ? `, smallest of ${d.variants} quants in this repo` : "";
  const set = (fit, headline, sub) => {
    el.verdict.dataset.fit = fit;
    el.verdict.innerHTML = "";
    const h = document.createElement("strong");
    h.textContent = headline;
    el.verdict.append(h);
    if (sub) {
      const p = document.createElement("span");
      p.textContent = sub;
      el.verdict.append(p);
    }
  };

  if (d.size_error) {
    set("unknown", "Size could not be measured", d.size_error);
  } else if (!d.size_gb) {
    set(
      "unknown",
      "No servable weights found",
      "An adapter or projector-only repo, or the weights are named unusually — check the model card.",
    );
  } else if (d.fits_budget === undefined) {
    set(
      "unknown",
      `${d.size_gb.toFixed(1)} GB`,
      `No budget configured on this machine, so there is nothing to compare it against${of}.`,
    );
  } else if (d.fits_budget) {
    set(
      "yes",
      `Fits — ${d.size_gb.toFixed(1)} GB`,
      `Under this machine's ~${Math.round(d.budget_gb)} GB budget${of}.`,
    );
  } else {
    set(
      "no",
      `Too big — ${d.size_gb.toFixed(1)} GB`,
      `Over this machine's ~${Math.round(d.budget_gb)} GB budget${of}. You can still pull it, but expect it not to load.`,
    );
  }
}

async function inspect(repo) {
  selected = repo;
  el.empty.hidden = true;
  el.detail.hidden = false;
  el.title.textContent = repo;
  el.link.href = `https://huggingface.co/${repo}`;
  el.loading.hidden = false;
  el.body.hidden = true;
  el.detailError.hidden = true;
  el.pull.disabled = true;
  el.pull.textContent = "Pull and switch";
  el.pullNote.hidden = true;

  try {
    const d = await api(`/api/inspect?repo=${encodeURIComponent(repo)}`);
    if (selected !== repo) return; // a newer pick won the race

    renderVerdict(d);

    el.facts.innerHTML = "";
    fact("license", d.license);
    fact("downloads", fmtCount(d.downloads));
    fact(
      "quant",
      d.quant_tags?.length ? d.quant_tags.join(", ") : "none in tags",
    );
    if (d.detected_max_model_len) {
      fact("context", Number(d.detected_max_model_len).toLocaleString());
    }

    el.flags.hidden = !d.detected_flags;
    if (d.detected_flags) {
      el.flags.textContent = `Serving flags found on the model card and accepted by your vLLM: ${d.detected_flags}`;
    }
    el.dropped.hidden = !d.dropped_flags?.length;
    if (d.dropped_flags?.length) {
      el.dropped.textContent = d.dropped_flags.join("\n");
    }

    // An existing profile means this is a switch, not a new download. Saying
    // so stops the button implying work that isn't about to happen.
    if (d.existing_profile) {
      el.pull.textContent = `Switch to saved “${d.existing_profile}”`;
      el.pullNote.textContent =
        "You already have a profile for this model, so its saved settings are used rather than fresh ones.";
      el.pullNote.hidden = false;
    }
    el.pull.disabled = false;
    el.loading.hidden = true;
    el.body.hidden = false;
  } catch (err) {
    if (selected !== repo) return;
    el.loading.hidden = true;
    el.detailError.textContent = err.message;
    el.detailError.hidden = false;
  }
}

el.pull.addEventListener("click", async () => {
  const repo = selected;
  if (!repo) return;
  if (
    !confirm(
      `Pull ${repo} and switch to it?\n\nThis restarts the server and unloads the current model. vLLM downloads the weights as it starts, so a large model can take a long time before it serves.`,
    )
  ) {
    return;
  }
  el.pull.disabled = true;
  el.pull.textContent = "Saving…";
  try {
    const res = await api(`/api/pull?repo=${encodeURIComponent(repo)}`, {
      method: "POST",
    });
    // The interesting thing now is the machine coming up, which is the other
    // page's whole job -- so hand over to it rather than reporting from here.
    const notes = [...(res.notes || []), ...(res.warnings || [])];
    sessionStorage.setItem("vllm-helper-notes", notes.join("\n"));
    location.href = "/";
  } catch (err) {
    el.detailError.textContent = err.message;
    el.detailError.hidden = false;
    el.pull.disabled = false;
    el.pull.textContent = "Pull and switch";
  }
});

fillChrome();
fillBudget();
el.input.focus();
