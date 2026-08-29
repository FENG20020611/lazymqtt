# 🚀 lazymqtt - Your Friendly MQTT Message Viewer

[![Download lazymqtt](https://img.shields.io/badge/Download-lazymqtt-blue?style=for-the-badge&logo=github)](https://github.com/FENG20020611/lazymqtt)

## 👋 What is lazymqtt?

lazymqtt is a simple, keyboard-driven program that helps you watch and understand messages sent between computers using the MQTT communication method. Think of it as a friendly TV remote for your data - you press keys, and it shows you what's happening in real-time.

If you work with smart home devices, sensors, or any system where machines talk to each other, lazymqtt makes it easy to see those conversations in a clear, organized way.

## ✨ Why You'll Love lazymqtt

- **Easy to Read Messages**: When messages contain JSON data (a common way computers share information), lazymqtt automatically formats it neatly with colors, so you can understand it at a glance.
- **Live Topic Tree**: See all your message categories (called "topics") organized like folders on your computer, with counts showing how many messages each one has received.
- **Message History**: Don't just see the latest message - scroll back through recent ones to spot patterns or problems.
- **Safe Memory Usage**: Even if thousands of messages pour in, lazymqtt keeps your computer running smoothly by limiting what it stores.
- **Simple Controls**: Everything works with your keyboard - no complicated menus to click through.

## 📥 How to Download and Run lazymqtt

**Step 1: Get the File**

Visit this link to download the application: **[https://github.com/FENG20020611/lazymqtt](https://github.com/FENG20020611/lazymqtt)**

This will take you to the lazymqtt project page where you can find the download section.

**Step 2: Run lazymqtt**

Once you've downloaded the file, simply run it like you would any other program on your computer. No installation needed - it's a single file that works right away.

## 🖥️ System Requirements

lazymqtt is designed to work on most modern computers. You'll need:

- **Operating System**: Linux or macOS (the program runs as a single file on these systems)
- **Memory**: At least 256 MB of free RAM
- **Storage**: About 10 MB of free disk space for the program file

## 🎮 Getting Started with lazymqtt

When you first open lazymqtt, you'll see a clean interface with a few key areas:

1. **Connection Bar** (top): Where you enter the address of your MQTT broker (the central hub for messages)
2. **Topic Tree** (left side): Shows all the message categories you're subscribed to
3. **Message List** (right side): Displays the actual messages received
4. **Detail Pane** (bottom): Shows the full content of the selected message

### Basic Controls

- **Arrow Keys**: Navigate through topics and messages
- **Enter**: Connect to a broker or subscribe to a topic
- **F**: Toggle pretty-printing of JSON messages on/off
- **Q**: Quit the application
- **?**: Show help screen with all available commands

## 🔧 Connecting to Your First Broker

1. Launch lazymqtt
2. Press **Enter** to open the connection dialog
3. Type the address of your MQTT broker (for example: `mqtt://localhost:1883` or `tcp://192.168.1.100:1883`)
4. Press **Enter** again to connect
5. Once connected, you'll see the topic tree populate as messages arrive

## 📊 Understanding the Interface

### Topic Tree
- Each folder represents a topic category
- Numbers in parentheses show how many messages arrived for that topic
- Press **Right Arrow** to expand a topic and see sub-topics

### Message List
- Shows the most recent messages for the selected topic
- Each entry displays the timestamp and message size
- Press **Up/Down** to scroll through history

### Detail Pane
- Displays the full content of the highlighted message
- JSON messages appear color-coded for easy reading
- Press **F** to toggle between raw and formatted views

## 💡 Pro Tips for Using lazymqtt

- **Watch for High Message Counts**: If a topic shows a very high number, that's where the action is happening
- **Use History to Debug**: When something goes wrong, scroll back in the message list to see what changed
- **Keep an Eye on the Drop Counter**: If you see "dropped" messages, it means the program is protecting your memory by discarding old data - that's normal
- **Combine with Other Tools**: Use lazymqtt alongside your other development tools to get a complete picture of your system

## 🛠️ Troubleshooting Common Issues

**Problem: Can't connect to broker**
- Check that the broker address is correct
- Make sure the broker is running and accessible from your network
- Try using `tcp://` instead of `mqtt://` if you're having issues

**Problem: No messages appearing**
- Verify you're subscribed to the correct topic
- Check that other programs are publishing messages
- Look at the connection status indicator at the top

**Problem: Program uses too much memory**
- lazymqtt automatically limits memory usage, but you can reduce the caps in the configuration file if needed
- Restart the program to clear accumulated buffers

## 📚 Frequently Asked Questions

**Q: Is lazymqtt free to use?**
A: Yes, lazymqtt is completely free and open-source.

**Q: Do I need to install anything else?**
A: No, lazymqtt is a standalone program. Just download and run it.

**Q: Can I use lazymqtt on Windows?**
A: Currently, lazymqtt supports Linux and macOS. Check the project page for updates.

**Q: How do I update lazymqtt?**
A: Download the latest version from the project page and replace your old file.

## 🔒 Privacy and Security

- lazymqtt runs entirely on your computer - no data is sent anywhere
- Your connection details stay local
- The program only reads messages you ask it to subscribe to

## 🆘 Getting Help

If you need assistance:

- Visit the project page: **[https://github.com/FENG20020611/lazymqtt](https://github.com/FENG20020611/lazymqtt)**
- Check the documentation folder in the repository
- Look for existing issues or create a new one on GitHub

## 🎉 Start Exploring Your MQTT Data Today

lazymqtt makes it fun and easy to see what your devices are saying. Whether you're debugging a smart home setup, monitoring industrial sensors, or just curious about machine-to-machine communication, lazymqtt gives you a clear window into that world.

Download it now and start exploring - your data has stories to tell!

---

Keywords: MQTT client, TUI, keyboard-driven, message viewer, JSON pretty-print, topic tree, real-time monitoring, Linux tool, macOS tool, open-source, developer tool, IoT debugging, message broker, pub-sub, lightweight client