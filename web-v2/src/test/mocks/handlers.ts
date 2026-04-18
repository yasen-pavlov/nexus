import { http, HttpResponse } from "msw";
import type {
  User,
  AuthResponse,
  HealthResponse,
  SearchResult,
  DocumentHit,
  RelatedResponse,
  ConnectorConfig,
  ConversationMessagesResponse,
  Document,
  IdentitiesResponse,
} from "@/lib/api-types";

const fakeAdmin: User = { id: "u1", username: "admin", role: "admin" };
const fakeUser: User = { id: "u2", username: "viewer", role: "user" };
const fakeToken = "fake-jwt-token";
const fakeUserToken = "fake-user-token";

function wrapData<T>(data: T) {
  return { data };
}

// Sample hits covering every source type so per-source card tests can look
// up representative fixtures by source_type.
const sampleHits: DocumentHit[] = [
  {
    id: "d-email-1",
    source_type: "imap",
    source_name: "personal-email",
    source_id: "INBOX:42",
    title: "Subject of an email",
    content: "Body preview…",
    metadata: {
      folder: "INBOX",
      from: "Alice <alice@example.com>",
      to: "me@example.com",
      date: "2026-04-10T10:00:00Z",
      has_attachments: true,
      attachment_filenames: ["invoice.pdf"],
    },
    visibility: "private",
    created_at: "2026-04-10T10:00:00Z",
    indexed_at: "2026-04-10T10:01:00Z",
    rank: 0.95,
    headline: "Body preview <em>match</em>…",
    related_count: 1,
  },
  {
    id: "d-telegram-1",
    source_type: "telegram",
    source_name: "tg-main",
    source_id: "12345:100-120",
    title: "Chat window",
    content: "Message preview",
    metadata: {
      chat_name: "Family",
      chat_id: 12345,
      first_message_id: 100,
      last_message_id: 120,
      message_count: 21,
      anchor_message_id: 100,
    },
    conversation_id: "12345",
    visibility: "private",
    created_at: "2026-04-05T18:00:00Z",
    indexed_at: "2026-04-05T18:05:00Z",
    rank: 0.85,
    headline: "Message <em>preview</em>",
  },
  {
    id: "d-paperless-1",
    source_type: "paperless",
    source_name: "paperless-main",
    source_id: "456",
    title: "Hospital invoice",
    content: "Bill content",
    mime_type: "application/pdf",
    size: 102400,
    metadata: {
      correspondent: "Hospital GmbH",
      document_type: "Invoice",
      tags: ["health", "2026"],
      original_file_name: "invoice_hospital.pdf",
    },
    url: "http://paperless:8000/documents/456",
    visibility: "shared",
    created_at: "2026-03-20T00:00:00Z",
    indexed_at: "2026-03-20T00:01:00Z",
    rank: 0.88,
    headline: "Bill <em>content</em>",
  },
  {
    id: "d-fs-1",
    source_type: "filesystem",
    source_name: "filesystem",
    source_id: "notes/meeting.md",
    title: "meeting.md",
    content: "Meeting notes",
    mime_type: "text/markdown",
    size: 2048,
    metadata: {
      path: "notes/meeting.md",
      size: 2048,
      extension: ".md",
      content_type: "text/markdown",
    },
    visibility: "shared",
    created_at: "2026-04-15T12:00:00Z",
    indexed_at: "2026-04-15T12:01:00Z",
    rank: 0.80,
    headline: "Meeting <em>notes</em>",
  },
];

export const handlers = [
  http.get("*/api/health", () =>
    HttpResponse.json(wrapData<HealthResponse>({ status: "ok" })),
  ),

  http.post("*/api/auth/login", async ({ request }) => {
    const body = (await request.json()) as {
      username: string;
      password: string;
    };
    if (body.username === "admin" && body.password === "password123") {
      return HttpResponse.json(
        wrapData<AuthResponse>({ token: fakeToken, user: fakeAdmin }),
      );
    }
    if (body.username === "viewer" && body.password === "password123") {
      return HttpResponse.json(
        wrapData<AuthResponse>({ token: fakeUserToken, user: fakeUser }),
      );
    }
    return HttpResponse.json({ error: "invalid credentials" }, { status: 400 });
  }),

  http.post("*/api/auth/register", async ({ request }) => {
    const body = (await request.json()) as {
      username: string;
      password: string;
    };
    return HttpResponse.json(
      wrapData<AuthResponse>({
        token: fakeToken,
        user: { ...fakeAdmin, username: body.username },
      }),
    );
  }),

  http.get("*/api/auth/me", ({ request }) => {
    const auth = request.headers.get("Authorization");
    if (auth === `Bearer ${fakeToken}`) {
      return HttpResponse.json(wrapData(fakeAdmin));
    }
    if (auth === `Bearer ${fakeUserToken}`) {
      return HttpResponse.json(wrapData(fakeUser));
    }
    return HttpResponse.json({ error: "Unauthorized" }, { status: 401 });
  }),

  http.get("*/api/search", ({ request }) => {
    const url = new URL(request.url);
    const q = url.searchParams.get("q") ?? "";
    const limit = Number(url.searchParams.get("limit") ?? 20);
    const offset = Number(url.searchParams.get("offset") ?? 0);
    const sources = url.searchParams.get("sources")?.split(",") ?? [];

    let filtered = [...sampleHits];
    if (sources.length) {
      filtered = filtered.filter((h) => sources.includes(h.source_type));
    }

    const page = filtered.slice(offset, offset + limit);
    const facets = {
      source_type: Array.from(
        filtered.reduce<Map<string, number>>((acc, h) => {
          acc.set(h.source_type, (acc.get(h.source_type) ?? 0) + 1);
          return acc;
        }, new Map()),
      ).map(([value, count]) => ({ value, count })),
      source_name: Array.from(
        filtered.reduce<Map<string, number>>((acc, h) => {
          acc.set(h.source_name, (acc.get(h.source_name) ?? 0) + 1);
          return acc;
        }, new Map()),
      ).map(([value, count]) => ({ value, count })),
    };

    return HttpResponse.json(
      wrapData<SearchResult>({
        documents: page,
        total_count: filtered.length,
        query: q,
        facets,
      }),
    );
  }),

  http.get("*/api/documents/:id/related", ({ params }) => {
    return HttpResponse.json(
      wrapData<RelatedResponse>({
        outgoing: [],
        incoming: params.id === "d-email-1" ? [
          {
            relation: { type: "attachment_of", target_source_id: "INBOX:42:attachment:0" },
            document: {
              id: "d-att-1",
              source_type: "imap",
              source_name: "personal-email",
              source_id: "INBOX:42:attachment:0",
              title: "invoice.pdf",
              content: "",
              visibility: "private",
              created_at: "2026-04-10T10:00:00Z",
              indexed_at: "2026-04-10T10:01:00Z",
            },
          },
        ] : [],
      }),
    );
  }),

  http.get("*/api/documents/:id/content", () => {
    return new HttpResponse(new Blob(["fake file content"], { type: "text/plain" }), {
      status: 200,
      headers: {
        "Content-Type": "text/plain",
        "Content-Disposition": 'attachment; filename="test.txt"',
      },
    });
  }),

  http.get("*/api/me/identities", () =>
    HttpResponse.json(
      wrapData<IdentitiesResponse>({
        identities: [
          {
            connector_id: "c-telegram-1",
            source_type: "telegram",
            source_name: "tg-main",
            external_id: "9001",
            external_name: "Me",
            has_avatar: false,
          },
        ],
      }),
    ),
  ),

  http.get("*/api/conversations/:sourceType/:conversationId/messages", () => {
    return HttpResponse.json(
      wrapData<ConversationMessagesResponse>({
        messages: sampleConversationMessages,
      }),
    );
  }),

  http.get("*/api/documents/by-source", ({ request }) => {
    const url = new URL(request.url);
    const sid = url.searchParams.get("source_id");
    const match = sampleConversationMessages.find((m) => m.source_id === sid);
    if (!match) {
      return HttpResponse.json({ error: "not found" }, { status: 404 });
    }
    return HttpResponse.json(wrapData<Document>(match));
  }),

  http.get("*/api/connectors/:id/avatars/:externalId", () =>
    new HttpResponse(null, { status: 404 }),
  ),

  http.get("*/api/connectors/", ({ request }) => {
    const auth = request.headers.get("Authorization");
    if (!auth) {
      return HttpResponse.json({ error: "Unauthorized" }, { status: 401 });
    }
    return HttpResponse.json(
      wrapData<ConnectorConfig[]>([
        {
          id: "c1",
          type: "imap",
          name: "personal-email",
          config: {},
          enabled: true,
          schedule: "",
          shared: false,
          status: "ok",
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
          last_run: "2026-04-10T00:00:00Z",
          user_id: "u1",
          external_id: "",
          external_name: "",
        },
      ]),
    );
  }),

  // Sync jobs list — empty by default; tests that care can override.
  http.get("*/api/sync", () => HttpResponse.json(wrapData([]))),
];

// sampleConversationMessages covers the Telegram-style chunks the
// conversation endpoint returns: two senders, a reply relation, and one
// message with an attachment. Kept as a module-level constant so tests
// can reason about the fixture.
const sampleConversationMessages: Document[] = [
  {
    id: "d-tg-msg-1",
    source_type: "telegram",
    source_name: "tg-main",
    source_id: "12345:100:msg",
    title: "Family",
    content: "Hey everyone, dinner at 7?",
    metadata: {
      chat_id: "12345",
      chat_name: "Family",
      message_id: 100,
      sender_id: 1001,
      sender_name: "Alice",
      sender_username: "alice_k",
    },
    relations: [],
    conversation_id: "12345",
    hidden: true,
    visibility: "private",
    created_at: "2026-04-10T18:00:00Z",
    indexed_at: "2026-04-10T18:05:00Z",
  },
  {
    id: "d-tg-msg-2",
    source_type: "telegram",
    source_name: "tg-main",
    source_id: "12345:101:msg",
    title: "Family",
    content: "Sounds good, I'll be there",
    metadata: {
      chat_id: "12345",
      chat_name: "Family",
      message_id: 101,
      sender_id: 9001,
      sender_name: "Me",
    },
    relations: [
      { type: "reply_to", target_source_id: "12345:100:msg" },
    ],
    conversation_id: "12345",
    hidden: true,
    visibility: "private",
    created_at: "2026-04-10T18:02:00Z",
    indexed_at: "2026-04-10T18:05:00Z",
  },
  {
    id: "d-tg-msg-3",
    source_type: "telegram",
    source_name: "tg-main",
    source_id: "12345:102:msg",
    title: "Family",
    content: "here's the receipt",
    metadata: {
      chat_id: "12345",
      chat_name: "Family",
      message_id: 102,
      sender_id: 1001,
      sender_name: "Alice",
      attachments: [
        {
          id: "d-tg-media-3",
          source_id: "12345:102:media",
          filename: "receipt.pdf",
          mime_type: "application/pdf",
          size: 24576,
        },
      ],
    },
    relations: [],
    conversation_id: "12345",
    hidden: true,
    visibility: "private",
    created_at: "2026-04-10T18:05:00Z",
    indexed_at: "2026-04-10T18:05:00Z",
  },
];

export {
  fakeAdmin,
  fakeUser,
  fakeToken,
  fakeUserToken,
  sampleHits,
  sampleConversationMessages,
};
