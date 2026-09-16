/**
 * Bottom tab bar — JS `<Tabs>` from expo-router (react-navigation under the
 * hood). We tried NativeTabs first but its `canPreventDefault: false`
 * constraint makes "tap More → open something" impossible. JS Tabs
 * supports `listeners.tabPress + e.preventDefault()`, the canonical RN
 * pattern for tab-as-action.
 *
 * The "More" tab is **not a navigation target** — its press opens a
 * DropdownMenu popover anchored above the tab. The popover is rendered
 * by `<MoreTabDropdownAnchor />` as a sibling of `<Tabs>`, NOT as a
 * `tabBarButton` replacement: keeping the real tab button intact means
 * the icon + "More" label render identically to the other three tabs.
 * We just open the dropdown imperatively from `listeners.tabPress` via
 * the exposed `TriggerRef.open()`.
 *
 * The stub (tabs)/more.tsx file still exists only because expo-router
 * requires every Tabs.Screen to have a backing route file — the press
 * is preventDefault'd so we never actually navigate to it.
 *
 * Active / inactive tint colors are derived from the current colour
 * scheme via THEME so dark mode picks contrasting values automatically.
 */
import { useRef } from "react";
import { Tabs } from "expo-router";
import { Image } from "expo-image";
import { View } from "react-native";
import type { TriggerRef } from "@rn-primitives/dropdown-menu";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import {
  useInboxUnreadCount,
  useChatUnreadMessageCount,
} from "@/lib/unread-counts";
import { MoreTabDropdownAnchor } from "@/components/nav/more-tab-dropdown";

// Only override backgroundColor — @react-navigation/elements Badge internally
// sets borderRadius = size/2, height = size, minWidth = size, so a single
// character renders as a perfect circle. Overriding minWidth/fontSize here
// breaks that geometry. Text color is auto-derived from backgroundColor
// luminance by Badge itself (white on brand blue).
const BADGE_STYLE = {
  backgroundColor: THEME.light.brand,
};

function InboxIcon({ color, size, focused }: { color: string; size: number; focused: boolean }) {
  return (
    <Image
      source={focused ? "sf:tray.fill" : "sf:tray"}
      tintColor={color}
      style={{ width: size, height: size }}
    />
  );
}

function MyIssuesIcon({ color, size, focused }: { color: string; size: number; focused: boolean }) {
  return (
    <Image
      source={focused ? "sf:checklist" : "sf:checklist.unchecked"}
      tintColor={color}
      style={{ width: size, height: size }}
    />
  );
}

function ChatIcon({ color, size, focused }: { color: string; size: number; focused: boolean }) {
  return (
    <Image
      source={focused ? "sf:bubble.left.fill" : "sf:bubble.left"}
      tintColor={color}
      style={{ width: size, height: size }}
    />
  );
}

function MoreIcon({ color, size }: { color: string; size: number }) {
  return (
    <Image
      source="sf:ellipsis"
      tintColor={color}
      style={{ width: size, height: size }}
    />
  );
}

export default function TabsLayout() {
  const { colorScheme } = useColorScheme();
  const t = THEME[colorScheme];

  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const inboxUnread = useInboxUnreadCount(wsId);
  const chatUnread = useChatUnreadMessageCount(wsId);

  // Truncation aligned with web's sidebar badges: 99+ for both. `undefined`
  // makes React Navigation hide the badge, so zero-count is a free no-op.
  let inboxBadge: string | undefined;
  if (inboxUnread > 99) {
    inboxBadge = "99+";
  } else if (inboxUnread > 0) {
    inboxBadge = String(inboxUnread);
  }

  let chatBadge: string | undefined;
  if (chatUnread > 99) {
    chatBadge = "99+";
  } else if (chatUnread > 0) {
    chatBadge = String(chatUnread);
  }

  // Imperative handle into the More tab's dropdown — listeners.tabPress
  // calls .open(); the @rn-primitives Trigger measures itself inside
  // open() so the popover anchors to MoreTabDropdownAnchor's rect.
  const moreTriggerRef = useRef<TriggerRef>(null);

  return (
    <View style={{ flex: 1 }}>
      <Tabs
        screenOptions={{
          headerShown: false,
          tabBarActiveTintColor: t.foreground,
          tabBarInactiveTintColor: t.mutedForeground,
          tabBarStyle: { backgroundColor: t.background },
          tabBarLabelStyle: { fontSize: 11 },
        }}
      >
        <Tabs.Screen
          name="inbox"
          options={{
            title: "Inbox",
            tabBarBadge: inboxBadge,
            tabBarBadgeStyle: BADGE_STYLE,
            tabBarIcon: InboxIcon,
          }}
        />
        <Tabs.Screen
          name="my-issues"
          options={{
            title: "My Issues",
            tabBarIcon: MyIssuesIcon,
          }}
        />
        <Tabs.Screen
          name="chat"
          options={{
            title: "Chat",
            tabBarBadge: chatBadge,
            tabBarBadgeStyle: BADGE_STYLE,
            tabBarIcon: ChatIcon,
          }}
        />
        <Tabs.Screen
          name="more"
          options={{
            title: "More",
            tabBarIcon: MoreIcon,
          }}
          listeners={() => ({
            tabPress: (e) => {
              // Don't navigate to the (stub) /more screen — open the
              // dropdown popover instead. The trigger is invisible and
              // mounted in MoreTabDropdownAnchor below; ref.open() also
              // measures its rect so the popover anchors correctly.
              e.preventDefault();
              moreTriggerRef.current?.open();
            },
          })}
        />
      </Tabs>

      <MoreTabDropdownAnchor triggerRef={moreTriggerRef} />
    </View>
  );
}
