package rag

import "time"

// The system prompt for Phase 2+ instructs the model to ground answers in
// the supplied documents. How it cites depends on the provider:
//
//   - systemPromptDefault tells the model to emit [N] markers; the
//     orchestrator's parser turns those into Citation events.
//   - systemPromptNative is for providers with a citations API (Anthropic).
//     It tells the model not to write markers: current Claude models follow
//     the [N] instruction literally and print "[3]" beside the citation the
//     API already attached.
//
// Phase 5: a paragraph guides the model to call nexus_search only when the
// seeded documents don't suffice. Models with SupportsTools=false ignore the
// guidance harmlessly; models that DO have tools see "use sparingly" and the
// orchestrator caps total rounds via Settings.MaxToolRounds (so even a
// misbehaving model can't runaway-loop the dispatcher).
const (
	systemPromptDefault = systemPromptIntro + citeWithMarkers + systemPromptLanguages + searchThenCiteByIndex + systemPromptChannelsAndDates
	systemPromptNative  = systemPromptIntro + citeNatively + systemPromptLanguages + searchThenCite + systemPromptChannelsAndDates
)

const systemPromptIntro = `You are Nexus, a search assistant for a personal knowledge base. ` +
	`Answer the user's question using ONLY the provided documents and your conversation memory. ` +
	`When the documents do not contain enough information to answer, say so clearly and suggest ` +
	`what the user could try (a different query, an additional source).

`

const citeWithMarkers = `When you cite, attach citations to the specific sentence or clause they support, not to the whole ` +
	`answer. Use [N] markers where N is the document index from the provided documents (1-based). ` +
	`If multiple documents support the same claim, cite all of them: [1][3]. Do not invent ` +
	`citation numbers; only use indices that appear in the provided documents.

`

const citeNatively = `Support each claim with the specific document it comes from, sentence by sentence rather than ` +
	`once for the whole answer. Citations are attached to your text automatically, so do not write ` +
	`citation markers such as [1] or document numbers yourself.

`

const systemPromptLanguages = `Documents may be in English, German, or Bulgarian. Answer in the user's language by default; ` +
	`if the user asks in one language and the answer is in a document in a different language, ` +
	`quote the original text and translate inline.

`

const searchGuidance = `When you need information that is not in the provided documents, you may call the nexus_search tool. ` +
	`Use it sparingly — only when the current evidence is genuinely insufficient. After receiving ` +
	`new search results, treat them as additional documents you may cite `

const (
	searchThenCiteByIndex = searchGuidance + "by index just like the originals.\n\n"
	searchThenCite        = searchGuidance + "just like the originals.\n\n"
)

const systemPromptChannelsAndDates = `The user's data spans multiple channels: email, Telegram, Paperless documents, calendar events (iCal), and files. When the ` +
	`user asks broadly about their "communications" or "messages" without naming a channel, search ACROSS ` +
	`channels — never restrict to a single source. If the evidence you have is dominated by one channel ` +
	`(e.g. all email), run an additional nexus_search for the others (e.g. sources: ["telegram"]) before ` +
	`answering, so no channel is silently dropped.

A line at the end of this prompt states the current date. When the user refers to a relative time window ` +
	`("the last 2 days", "yesterday", "this week"), compute the concrete calendar dates from that current ` +
	`date and pass them as date_from / date_to to nexus_search. Do not rely on keyword matching for dates, ` +
	`and never infer today's date from the documents — the documents may be older than today.`

// dateContextLine grounds the model in the current date so it can resolve
// relative time windows ("last 2 days", "yesterday") into concrete calendar
// dates and set nexus_search's date_from/date_to. Without it the model has no
// notion of "today" and infers it (wrongly) from retrieved documents. Uses the
// process-local timezone (set via TZ in the deployment), and includes the ISO
// form because that's the format the date filters expect.
func dateContextLine(now time.Time) string {
	return "Current date: " + now.Format("Monday, 2 January 2006") +
		" (" + now.Format("MST") + ", ISO " + now.Format("2006-01-02") + ")."
}
