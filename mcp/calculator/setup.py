#!/usr/bin/env python3
'''
Setup script for Calculator MCP Server
'''

import subprocess
import sys
import os

def install_dependencies():
    '''Install required Python packages.'''
    print("Installing dependencies...")
    
    # Check if pip is available
    try:
        subprocess.run([sys.executable, "-m", "pip", "--version"], 
                      check=True, capture_output=True)
    except subprocess.CalledProcessError:
        print("Error: pip not found. Please install pip first.")
        return False
    
    # Install from requirements.txt
    if os.path.exists("requirements.txt"):
        try:
            subprocess.run([sys.executable, "-m", "pip", "install", "-r", "requirements.txt"],
                          check=True)
            print("Dependencies installed successfully!")
            return True
        except subprocess.CalledProcessError as e:
            print(f"Error installing dependencies: {e}")
            return False
    else:
        print("requirements.txt not found. Installing default packages...")
        try:
            packages = ["mcp[fastmcp]", "pydantic", "httpx"]
            subprocess.run([sys.executable, "-m", "pip", "install"] + packages,
                          check=True)
            print("Default packages installed successfully!")
            return True
        except subprocess.CalledProcessError as e:
            print(f"Error installing packages: {e}")
            return False

def verify_installation():
    '''Verify that all required packages are installed.'''
    print("\nVerifying installation...")
    
    required_packages = ["mcp", "pydantic", "httpx"]
    missing_packages = []
    
    for package in required_packages:
        try:
            __import__(package.replace("-", "_"))
            print(f"✓ {package}")
        except ImportError:
            missing_packages.append(package)
            print(f"✗ {package} (missing)")
    
    if missing_packages:
        print(f"\nMissing packages: {', '.join(missing_packages)}")
        print("Please install them manually:")
        print(f"pip install {' '.join(missing_packages)}")
        return False
    
    print("\nAll packages installed successfully!")
    return True

def run_tests():
    '''Run the test script.'''
    print("\nRunning tests...")
    
    if os.path.exists("test_calculator.py"):
        try:
            subprocess.run([sys.executable, "test_calculator.py"], check=True)
            print("\nTests completed successfully!")
            return True
        except subprocess.CalledProcessError as e:
            print(f"\nTests failed: {e}")
            return False
    else:
        print("test_calculator.py not found.")
        return False

def show_usage():
    '''Show usage instructions.'''
    print("\n" + "="*60)
    print("Calculator MCP Server - Usage Instructions")
    print("="*60)
    print("\n1. Run the MCP server:")
    print("   python calculator_mcp.py")
    print("\n2. Test with MCP Inspector:")
    print("   npx @modelcontextprotocol/inspector python calculator_mcp.py")
    print("\n3. Available tools:")
    print("   - calculator_basic_operation")
    print("   - calculator_advanced_math")
    print("   - calculator_trigonometric")
    print("   - calculator_statistics")
    print("   - calculator_unit_conversion")
    print("\n4. Test the tools directly:")
    print("   python test_calculator.py")
    print("\n5. View this help again:")
    print("   python setup.py --help")
    print("\n" + "="*60)

def main():
    '''Main setup function.'''
    import argparse
    
    parser = argparse.ArgumentParser(description="Calculator MCP Server Setup")
    parser.add_argument("--install", action="store_true", help="Install dependencies")
    parser.add_argument("--verify", action="store_true", help="Verify installation")
    parser.add_argument("--test", action="store_true", help="Run tests")
    parser.add_argument("--all", action="store_true", help="Run all setup steps")
    
    args = parser.parse_args()
    
    if not any([args.install, args.verify, args.test, args.all]):
        show_usage()
        return
    
    success = True
    
    if args.install or args.all:
        success = install_dependencies() and success
    
    if args.verify or args.all:
        success = verify_installation() and success
    
    if args.test or args.all:
        success = run_tests() and success
    
    if args.all:
        show_usage()
    
    if success:
        print("\nSetup completed successfully!")
        sys.exit(0)
    else:
        print("\nSetup failed. Please check the errors above.")
        sys.exit(1)

if __name__ == "__main__":
    main()