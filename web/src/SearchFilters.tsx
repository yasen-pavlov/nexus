import { type Facet, type SearchFilters } from './api';

interface Props {
  facets?: Record<string, Facet[]>;
  filters: SearchFilters;
  onChange: (filters: SearchFilters) => void;
}

export default function SearchFiltersBar({ facets, filters, onChange }: Props) {
  const sourceTypeFacets = facets?.source_type || [];
  const sourceNameFacets = facets?.source_name || [];

  const toggleSource = (source: string) => {
    const current = filters.sources || [];
    const updated = current.includes(source)
      ? current.filter((s) => s !== source)
      : [...current, source];
    onChange({ ...filters, sources: updated.length > 0 ? updated : undefined });
  };

  const toggleSourceName = (name: string) => {
    const current = filters.sourceNames || [];
    const updated = current.includes(name)
      ? current.filter((s) => s !== name)
      : [...current, name];
    onChange({ ...filters, sourceNames: updated.length > 0 ? updated : undefined });
  };

  const hasFilters = (filters.sources?.length || 0) > 0
    || (filters.sourceNames?.length || 0) > 0
    || !!filters.dateFrom
    || !!filters.dateTo;

  return (
    <div className="search-filters">
      {sourceTypeFacets.length > 0 && (
        <div className="filter-group">
          <span className="filter-label">Source:</span>
          {sourceTypeFacets.map((f) => (
            <button
              key={f.value}
              className={`filter-chip ${filters.sources?.includes(f.value) ? 'active' : ''}`}
              onClick={() => toggleSource(f.value)}
            >
              {f.value} <span className="filter-count">{f.count}</span>
            </button>
          ))}
        </div>
      )}

      {sourceNameFacets.length > sourceTypeFacets.length && (
        <div className="filter-group">
          <span className="filter-label">Connector:</span>
          {sourceNameFacets.map((f) => (
            <button
              key={f.value}
              className={`filter-chip ${filters.sourceNames?.includes(f.value) ? 'active' : ''}`}
              onClick={() => toggleSourceName(f.value)}
            >
              {f.value} <span className="filter-count">{f.count}</span>
            </button>
          ))}
        </div>
      )}

      <div className="filter-group">
        <span className="filter-label">Date:</span>
        <input
          type="date"
          className="filter-date"
          value={filters.dateFrom || ''}
          onChange={(e) => onChange({ ...filters, dateFrom: e.target.value || undefined })}
        />
        <span className="filter-separator">to</span>
        <input
          type="date"
          className="filter-date"
          value={filters.dateTo || ''}
          onChange={(e) => onChange({ ...filters, dateTo: e.target.value || undefined })}
        />
      </div>

      {hasFilters && (
        <button
          className="filter-clear"
          onClick={() => onChange({})}
        >
          Clear filters
        </button>
      )}
    </div>
  );
}
