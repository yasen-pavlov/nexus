export function connectorTypeLabel(type: string): string {
  switch (type) {
    case "filesystem":
      return "Filesystem";
    case "imap":
      return "Email · IMAP";
    case "paperless":
      return "Paperless-ngx";
    case "telegram":
      return "Telegram";
    case "ical":
      return "Calendar · iCloud";
    default:
      return type;
  }
}
