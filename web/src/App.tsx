import { useState } from 'react'

const tabs = ['Overview', 'Chat', 'Studio', 'Workspace', 'Kanban', 'Memory', 'Goals', 'Pipeline'] as const
type Tab = (typeof tabs)[number]

function App() {
  const [activeTab, setActiveTab] = useState<Tab>('Overview')

  return (
    <div className="flex h-screen bg-gray-950 text-gray-100">
      {/* Left Sidebar */}
      <aside className="w-64 bg-gray-900 flex-shrink-0 flex flex-col border-r border-gray-800">
        <div className="p-4 border-b border-gray-800">
          <h1 className="text-lg font-semibold text-white">Agent OS</h1>
        </div>
        <nav className="flex-1 p-4 space-y-2">
          <p className="text-sm text-gray-500 uppercase tracking-wider mb-2">Navigation</p>
          <div className="px-3 py-2 rounded bg-gray-800 text-white text-sm">Dashboard</div>
          <div className="px-3 py-2 rounded text-gray-400 hover:text-white hover:bg-gray-800 text-sm cursor-pointer">Agents</div>
          <div className="px-3 py-2 rounded text-gray-400 hover:text-white hover:bg-gray-800 text-sm cursor-pointer">Settings</div>
        </nav>
      </aside>

      {/* Main Content Area */}
      <div className="flex-1 flex flex-col min-w-0">
        {/* Top Tab Bar */}
        <header className="bg-gray-900 border-b border-gray-800 flex-shrink-0">
          <div className="flex items-center gap-1 px-4 pt-2">
            {tabs.map((tab) => (
              <button
                key={tab}
                onClick={() => setActiveTab(tab)}
                className={`px-4 py-2 text-sm font-medium rounded-t transition-colors ${
                  activeTab === tab
                    ? 'bg-gray-950 text-white border-t border-l border-r border-gray-800'
                    : 'text-gray-400 hover:text-gray-200 hover:bg-gray-800'
                }`}
              >
                {tab}
              </button>
            ))}
          </div>
        </header>

        {/* Content */}
        <main className="flex-1 p-6 overflow-auto">
          <h2 className="text-2xl font-bold mb-4">{activeTab}</h2>
          <p className="text-gray-400">
            This is the {activeTab} panel. Content will be added here.
          </p>
        </main>
      </div>
    </div>
  )
}

export default App
