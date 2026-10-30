import { useState } from 'react';
import { NavLink } from 'react-router-dom';
import {
  LayoutDashboard, GitBranch, Play, CheckSquare,
  AlertTriangle, History, LogOut, Workflow, Users, Shield, BarChart3, PenTool, FlaskConical,
  PanelLeftClose, PanelLeftOpen
} from 'lucide-react';
import { useAuth } from '../../hooks/useAuth';

const navItems = [
  { to: '/',           icon: LayoutDashboard, label: 'Dashboard' },
  { to: '/processes',  icon: GitBranch,        label: 'Definitions' },
  { to: '/modeler',    icon: PenTool,          label: 'Modeler' },
  { to: '/instances',  icon: Play,             label: 'Instances' },
  { to: '/tasks',      icon: CheckSquare,      label: 'Tasks' },
  { to: '/incidents',  icon: AlertTriangle,    label: 'Incidents' },
  { to: '/history',    icon: History,          label: 'History' },
  { to: '/analytics',  icon: BarChart3,        label: 'Analytics' },
  { to: '/simulation', icon: FlaskConical,     label: 'Simulation lab' },
];

const adminNavItems = [
  { to: '/admin/users', icon: Users,  label: 'Users' },
  { to: '/admin/roles', icon: Shield, label: 'Roles' },
];

function NavItems({ items, collapsed }: { items: typeof navItems; collapsed: boolean }) {
  return (
    <>
      {items.map(({ to, icon: Icon, label }) => (
        <NavLink
          key={to}
          to={to}
          end={to === '/'}
          aria-label={label}
          title={collapsed ? label : undefined}
          className={({ isActive }) =>
            `flex items-center mx-2 py-2.5 rounded-lg text-sm transition-colors focus-visible:outline-2 focus-visible:outline-blue-400 ${collapsed ? 'justify-center' : 'gap-3 px-3'} ${
              isActive
                ? 'bg-blue-600 text-white font-medium'
                : 'text-gray-400 hover:bg-gray-800 hover:text-white'
            }`
          }
        >
          <Icon aria-hidden="true" className="w-4 h-4 flex-shrink-0" />
          <span className={collapsed ? 'sr-only' : 'whitespace-nowrap'}>{label}</span>
        </NavLink>
      ))}
    </>
  );
}

export function Sidebar() {
  const { user, logout, hasAction } = useAuth();
  const showAdmin = hasAction('CAN_MANAGE_USER') || hasAction('CAN_EDIT_ROLE');
  const [collapsed, setCollapsed] = useState(() => {
    try { return localStorage.getItem('bpmngo.sidebar.collapsed') === 'true'; }
    catch { return false; }
  });
  const toggleSidebar = () => {
    const next = !collapsed;
    setCollapsed(next);
    try { localStorage.setItem('bpmngo.sidebar.collapsed', String(next)); }
    catch { /* Keep the toggle usable when browser storage is unavailable. */ }
  };

  return (
    <aside aria-label="Main sidebar" className={`${collapsed ? 'w-16' : 'w-56'} flex-shrink-0 bg-gray-900 flex flex-col h-screen sticky top-0 transition-[width] duration-200 motion-reduce:transition-none`}>
      {/* Logo */}
      <div className={`flex items-center h-16 flex-shrink-0 border-b border-gray-800 ${collapsed ? 'justify-center' : 'gap-2.5 px-4'}`}>
        {!collapsed && <Workflow aria-hidden="true" className="w-6 h-6 flex-shrink-0 text-blue-400" />}
        <span className={collapsed ? 'sr-only' : 'text-white font-bold text-base tracking-tight'}>BpmnGo</span>
        <button type="button" onClick={toggleSidebar}
          aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          aria-expanded={!collapsed} aria-controls="main-navigation"
          className={`rounded-lg p-2 text-gray-400 hover:bg-gray-800 hover:text-white focus-visible:outline-2 focus-visible:outline-blue-400 ${collapsed ? '' : 'ml-auto'}`}>
          {collapsed ? <PanelLeftOpen aria-hidden="true" className="w-4 h-4" /> : <PanelLeftClose aria-hidden="true" className="w-4 h-4" />}
        </button>
      </div>

      {/* Nav */}
      <nav id="main-navigation" aria-label="Main navigation" className="flex-1 py-4 overflow-y-auto">
        <NavItems items={navItems} collapsed={collapsed} />
        {showAdmin && (
          <>
            <div className={collapsed ? 'mx-3 mt-4 mb-2 border-t border-gray-800' : 'mt-4 mb-1 px-5'}>
              <p className={collapsed ? 'sr-only' : 'text-xs font-semibold text-gray-600 uppercase tracking-wider'}>Admin</p>
            </div>
            <NavItems items={adminNavItems} collapsed={collapsed} />
          </>
        )}
      </nav>

      {/* User */}
      <div className={`border-t border-gray-800 ${collapsed ? 'px-2 py-4' : 'p-4'}`}>
        <div className={`flex items-center mb-3 ${collapsed ? 'justify-center' : 'gap-3'}`} title={collapsed ? user?.full_name || user?.email : undefined}>
          <div aria-hidden="true" className="w-8 h-8 flex-shrink-0 rounded-full bg-blue-500 flex items-center justify-center text-white text-xs font-bold">
            {(user?.full_name?.[0] ?? user?.email?.[0] ?? '?').toUpperCase()}
          </div>
          <div className={collapsed ? 'sr-only' : 'min-w-0'}>
            <p className="text-white text-xs font-medium truncate">
              {user?.full_name || user?.email}
            </p>
            <p className="text-gray-500 text-xs truncate">{user?.email}</p>
          </div>
        </div>
        <button
          type="button"
          onClick={logout}
          aria-label="Sign out"
          title={collapsed ? 'Sign out' : undefined}
          className={`flex items-center rounded-lg text-gray-400 hover:text-white text-xs w-full focus-visible:outline-2 focus-visible:outline-blue-400 ${collapsed ? 'justify-center py-2.5 hover:bg-gray-800' : 'gap-2'}`}
        >
          <LogOut aria-hidden="true" className="w-3.5 h-3.5" /><span className={collapsed ? 'sr-only' : ''}>Sign out</span>
        </button>
      </div>
    </aside>
  );
}
