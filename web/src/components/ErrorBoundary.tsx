import { Component, type ReactNode } from 'react'

interface Props {
  children: ReactNode
  fallback?: ReactNode
  name?: string
}

interface State {
  hasError: boolean
  error: Error | null
}

export class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props)
    this.state = { hasError: false, error: null }
  }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error }
  }

  render() {
    if (this.state.hasError) {
      if (this.props.fallback) return this.props.fallback
      return (
        <div className="p-6 rounded-lg border border-red-900/50 bg-red-950/20">
          <h3 className="text-red-400 font-medium mb-2">
            {this.props.name ? `Error in ${this.props.name}` : 'Something went wrong'}
          </h3>
          <p className="text-sm text-red-300/70 mb-3">
            {this.state.error?.message ?? 'An unexpected error occurred.'}
          </p>
          <button
            onClick={() => this.setState({ hasError: false, error: null })}
            className="text-sm px-3 py-1.5 bg-gray-800 hover:bg-gray-700 rounded transition-colors text-gray-200"
          >
            Try Again
          </button>
        </div>
      )
    }
    return this.props.children
  }
}
