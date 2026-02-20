#!/usr/bin/env python3
'''
Example usage of Calculator MCP Server

This script demonstrates how to use the calculator MCP server tools
programmatically.
'''

import asyncio
import json
from calculator_mcp import (
    calculator_basic_operation,
    calculator_advanced_math,
    calculator_trigonometric,
    calculator_statistics,
    calculator_unit_conversion,
    BasicOperationInput,
    AdvancedMathInput,
    TrigonometricInput,
    StatisticsInput,
    UnitConversionInput,
    OperationType,
    AdvancedOperationType,
    TrigonometricOperationType,
    UnitType,
    ResponseFormat,
    TemperatureUnit,
    LengthUnit,
    WeightUnit
)

async def demonstrate_basic_operations():
    '''Demonstrate basic arithmetic operations.'''
    print("=== Basic Arithmetic Operations ===\n")
    
    # Example 1: Simple addition
    print("1. Addition: 12.5 + 7.3")
    params = BasicOperationInput(
        operation=OperationType.ADD,
        a=12.5,
        b=7.3,
        response_format=ResponseFormat.MARKDOWN
    )
    result = await calculator_basic_operation(params)
    print(result)
    print()
    
    # Example 2: Division with error handling
    print("2. Division: 20 / 4")
    params = BasicOperationInput(
        operation=OperationType.DIVIDE,
        a=20,
        b=4,
        response_format=ResponseFormat.JSON
    )
    result = await calculator_basic_operation(params)
    print(json.loads(result))
    print()

async def demonstrate_advanced_math():
    '''Demonstrate advanced mathematical functions.'''
    print("=== Advanced Mathematical Functions ===\n")
    
    # Example 1: Calculate area of circle (πr²)
    print("1. Area of circle with radius 5:")
    params = AdvancedMathInput(
        operation=AdvancedOperationType.POWER,
        value=5,
        exponent=2,
        response_format=ResponseFormat.MARKDOWN
    )
    radius_squared = await calculator_advanced_math(params)
    print(f"Radius squared: {json.loads(radius_squared)['result']}")
    
    # Multiply by π
    import math
    area = json.loads(radius_squared)['result'] * math.pi
    print(f"Area = π × r² = {area:.4f}")
    print()
    
    # Example 2: Compound interest calculation
    print("2. Compound interest: $1000 at 5% for 3 years")
    principal = 1000
    rate = 0.05
    years = 3
    
    # Calculate (1 + rate)^years
    params = AdvancedMathInput(
        operation=AdvancedOperationType.POWER,
        value=1 + rate,
        exponent=years,
        response_format=ResponseFormat.JSON
    )
    growth_factor = await calculator_advanced_math(params)
    growth = json.loads(growth_factor)['result']
    
    final_amount = principal * growth
    print(f"Growth factor: {growth:.4f}")
    print(f"Final amount: ${final_amount:.2f}")
    print()

async def demonstrate_trigonometry():
    '''Demonstrate trigonometric functions.'''
    print("=== Trigonometric Functions ===\n")
    
    # Example 1: Right triangle calculations
    print("1. Right triangle with angle 30°, hypotenuse 10:")
    angle = 30
    hypotenuse = 10
    
    # Calculate opposite side (hypotenuse × sin(angle))
    params = TrigonometricInput(
        operation=TrigonometricOperationType.SINE,
        angle=angle,
        use_radians=False,
        response_format=ResponseFormat.JSON
    )
    sin_result = await calculator_trigonometric(params)
    sin_value = json.loads(sin_result)['result']
    
    opposite = hypotenuse * sin_value
    print(f"sin({angle}°) = {sin_value:.4f}")
    print(f"Opposite side = {hypotenuse} × {sin_value:.4f} = {opposite:.2f}")
    print()
    
    # Example 2: Angle from slope
    print("2. Angle of slope with rise 3, run 4:")
    rise = 3
    run = 4
    slope = rise / run
    
    params = TrigonometricInput(
        operation=TrigonometricOperationType.ARCTANGENT,
        angle=slope,
        use_radians=False,
        response_format=ResponseFormat.MARKDOWN
    )
    result = await calculator_trigonometric(params)
    print(result)
    print()

async def demonstrate_statistics():
    '''Demonstrate statistical calculations.'''
    print("=== Statistical Calculations ===\n")
    
    # Example 1: Exam scores analysis
    print("1. Exam scores analysis:")
    scores = [85, 92, 78, 90, 88, 95, 82, 87, 91, 89]
    
    # Calculate mean
    params = StatisticsInput(
        operation="mean",
        values=scores,
        response_format=ResponseFormat.JSON
    )
    mean_result = await calculator_statistics(params)
    mean = json.loads(mean_result)['result']
    
    # Calculate standard deviation
    params = StatisticsInput(
        operation="stdev",
        values=scores,
        response_format=ResponseFormat.JSON
    )
    stdev_result = await calculator_statistics(params)
    stdev = json.loads(stdev_result)['result']
    
    print(f"Scores: {scores}")
    print(f"Mean: {mean:.2f}")
    print(f"Standard deviation: {stdev:.2f}")
    print(f"Range: {min(scores)} - {max(scores)}")
    print()
    
    # Example 2: Temperature data
    print("2. Daily temperatures (°C):")
    temperatures = [22.5, 23.1, 21.8, 24.2, 22.9, 23.5, 22.1]
    
    params = StatisticsInput(
        operation="median",
        values=temperatures,
        response_format=ResponseFormat.MARKDOWN
    )
    result = await calculator_statistics(params)
    print(result)
    print()

async def demonstrate_unit_conversions():
    '''Demonstrate unit conversions.'''
    print("=== Unit Conversions ===\n")
    
    # Example 1: Cooking conversions
    print("1. Cooking conversions:")
    
    # Convert 2 cups of flour to grams (1 cup ≈ 125g)
    cups = 2
    grams_per_cup = 125
    
    params = UnitConversionInput(
        unit_type=UnitType.WEIGHT,
        value=cups * grams_per_cup,
        from_unit=WeightUnit.GRAM,
        to_unit=WeightUnit.OUNCE,
        response_format=ResponseFormat.MARKDOWN
    )
    result = await calculator_unit_conversion(params)
    print(f"{cups} cups of flour ({cups * grams_per_cup}g) in ounces:")
    print(result)
    print()
    
    # Example 2: Temperature for baking
    print("2. Baking temperature conversion:")
    
    params = UnitConversionInput(
        unit_type=UnitType.TEMPERATURE,
        value=180,
        from_unit=TemperatureUnit.CELSIUS,
        to_unit=TemperatureUnit.FAHRENHEIT,
        response_format=ResponseFormat.JSON
    )
    result = await calculator_unit_conversion(params)
    data = json.loads(result)
    print(f"180°C (common baking temperature) = {data['result']:.1f}°F")
    print()
    
    # Example 3: Room dimensions
    print("3. Room dimensions conversion:")
    
    params = UnitConversionInput(
        unit_type=UnitType.LENGTH,
        value=4,
        from_unit=LengthUnit.METER,
        to_unit=LengthUnit.FOOT,
        response_format=ResponseFormat.MARKDOWN
    )
    result = await calculator_unit_conversion(params)
    print("4 meters in feet:")
    print(result)

async def real_world_scenario():
    '''Demonstrate a real-world calculation scenario.'''
    print("\n" + "="*60)
    print("Real-World Scenario: Home Renovation Project")
    print("="*60 + "\n")
    
    print("You're planning to install new flooring in a room that is:")
    print("- Length: 5.5 meters")
    print("- Width: 4.2 meters")
    print("\nFlooring tiles are sold in boxes that cover 2.5 square meters each.")
    print("Let's calculate how many boxes you need:\n")
    
    # Calculate area in square meters
    params = BasicOperationInput(
        operation=OperationType.MULTIPLY,
        a=5.5,
        b=4.2,
        response_format=ResponseFormat.JSON
    )
    area_result = await calculator_basic_operation(params)
    area = json.loads(area_result)['result']
    print(f"1. Room area: {area:.2f} square meters")
    
    # Calculate number of boxes needed
    boxes_needed = area / 2.5
    
    params = AdvancedMathInput(
        operation=AdvancedOperationType.CEIL,
        value=boxes_needed,
        response_format=ResponseFormat.JSON
    )
    boxes_result = await calculator_advanced_math(params)
    boxes = json.loads(boxes_result)['result']
    
    print(f"2. Boxes needed: {area:.2f} ÷ 2.5 = {boxes_needed:.2f}")
    print(f"   Round up to: {boxes} boxes")
    
    # Convert to square feet for US reference
    params = UnitConversionInput(
        unit_type=UnitType.LENGTH,
        value=5.5,
        from_unit=LengthUnit.METER,
        to_unit=LengthUnit.FOOT,
        response_format=ResponseFormat.JSON
    )
    length_ft = json.loads(await calculator_unit_conversion(params))['result']
    
    params = UnitConversionInput(
        unit_type=UnitType.LENGTH,
        value=4.2,
        from_unit=LengthUnit.METER,
        to_unit=LengthUnit.FOOT,
        response_format=ResponseFormat.JSON
    )
    width_ft = json.loads(await calculator_unit_conversion(params))['result']
    
    area_ft = length_ft * width_ft
    print(f"\n3. For reference in US measurements:")
    print(f"   Room dimensions: {length_ft:.1f} ft × {width_ft:.1f} ft")
    print(f"   Room area: {area_ft:.1f} square feet")
    
    print("\n" + "="*60)

async def main():
    '''Run all demonstrations.'''
    print("Calculator MCP Server - Example Usage\n")
    
    await demonstrate_basic_operations()
    await demonstrate_advanced_math()
    await demonstrate_trigonometry()
    await demonstrate_statistics()
    await demonstrate_unit_conversions()
    await real_world_scenario()
    
    print("\nAll examples completed successfully!")
    print("\nTo use these tools in an MCP client, run:")
    print("  python calculator_mcp.py")
    print("\nOr test with MCP Inspector:")
    print("  npx @modelcontextprotocol/inspector python calculator_mcp.py")

if __name__ == "__main__":
    asyncio.run(main())